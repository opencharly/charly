package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/opencharly/sdk/deploykit"
	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk/spec"
)

// executorReverseServer is the HOST side of the E3b reverse channel: it serves the
// proto ExecutorService by delegating to a live DeployExecutor (ShellExecutor /
// SSHExecutor / NestedExecutor). A deploy/step/builder provider's executor holds live
// OS resources (shell FDs, SSH connections) that cannot cross a process boundary, so
// an OUT-OF-PROCESS plugin never holds it — instead the host stands one of these up on
// the go-plugin GRPCBroker per deploy Invoke, passes its broker id in
// InvokeRequest.executor_broker_id, and the plugin dials back to run ops on the real
// venue. Built-in providers use the typed DeployExecutor directly (no wire).
type executorReverseServer struct {
	pb.UnimplementedExecutorServiceServer
	exec deploykit.DeployExecutor
	// build is the host BUILD-ENGINE context (project Config + dir) the RunHostStep host-engine
	// leg needs to run a BuilderStep's host build (EnsureImagePresent + BuilderRun resolve
	// a short / namespace-qualified builder image and fall back to a local `charly box
	// build`). Zero value for a verb/kind/deploy Invoke that never drives RunHostStep.
	build buildEngineContext
	// rebootable marks the venue as a charly-owned guest a RebootStep may reboot mid-walk (a
	// VM, set by the vm deploy substrate via InvokeWithExecutor). false (the default) makes a
	// RebootStep skip-and-note — a host venue (target:local, host:local or host:user@machine)
	// is NEVER rebooted by a plugin walk (the prior in-proc LocalDeployTarget skipped+warned).
	rebootable bool
	// live carries the LIVE (non-serializable) overlay-build inputs — the compiled InstallPlans +
	// the nested-venue parent executor/node — a pod lifecycle plugin's HostBuild("overlay") needs
	// but cannot receive across the []byte wire (M4). The host proxy attaches it (via the Invoke
	// ctx, read by InvokeWithExecutor); the HostBuild handler re-threads it onto the builder ctx.
	// nil for every non-lifecycle Invoke.
	live *overlayBuildInputs
}

// HostArbiter is the C9 resource-arbiter reverse leg: the COMPILED-IN candy/plugin-preempt
// (verb:arbiter) calls back mid-logic for its 2 remaining genuinely K1-blocked host
// dependencies (gather/resources — both LoadUnified-coupled; the other 6 original seams moved
// into the plugin itself, FLOOR-SLIM-proper Unit-8). It decodes the action-tagged request and
// delegates to the stateless arbiterHostServer. ALWAYS served — like HostBuild / InvokeProvider —
// because the arbiter is now reached over TWO reverse-channel topologies: the in-core proxy's
// executor AND a compiled-in command:preempt calling InvokeProvider(verb:arbiter) over the
// generic command dispatch's bare executor. The seam impls are stateless (touch no s.exec), so serving
// it unconditionally is inert on every non-arbiter channel (only verb:arbiter ever sends this RPC) —
// strictly cleaner than injecting arbiter-specific wiring into the generic command dispatch. A seam OP
// failure rides the seam's own reply; the RPC error field is reserved for infra failure (bad action).
func (s *executorReverseServer) HostArbiter(ctx context.Context, req *pb.HostArbiterRequest) (*pb.HostArbiterReply, error) {
	out, err := newArbiterHostServer().dispatch(req.GetAction())
	if err != nil {
		return &pb.HostArbiterReply{Error: err.Error()}, nil
	}
	return &pb.HostArbiterReply{ResultJson: out}, nil
}

func (s *executorReverseServer) Venue(context.Context, *pb.Empty) (*pb.VenueReply, error) {
	return &pb.VenueReply{Venue: s.exec.Venue()}, nil
}

func (s *executorReverseServer) RunSystem(ctx context.Context, req *pb.RunRequest) (*pb.RunReply, error) {
	return runReply(s.exec.RunSystem(ctx, req.GetScript(), decodeReverseEmitOpts(req.GetOptsJson())))
}

func (s *executorReverseServer) RunUser(ctx context.Context, req *pb.RunRequest) (*pb.RunReply, error) {
	return runReply(s.exec.RunUser(ctx, req.GetScript(), decodeReverseEmitOpts(req.GetOptsJson())))
}

// PutFile is the deploy/step file-PLACEMENT leg: an OUT-OF-PROCESS deploy/step plugin
// that EXECUTES an InstallPlan's steps pushes file content (a service unit, an env.d
// file, the charly binary, a builder artifact) onto the venue. The plugin holds no
// venue filesystem across the process boundary, so it ships the bytes; the host
// materializes them to a private temp file and delegates to the live
// DeployExecutor.PutFile (a plain os.WriteFile for ShellExecutor, scp+install for
// SSHExecutor), preserving the owner_root → root:root semantics. The gRPC call itself
// succeeds; a placement failure travels in PutFileReply.Error (the runReply convention).
func (s *executorReverseServer) PutFile(ctx context.Context, req *pb.PutFileRequest) (*pb.PutFileReply, error) {
	tmp, err := os.CreateTemp("", "charly-putfile-*")
	if err != nil {
		return &pb.PutFileReply{Error: err.Error()}, nil
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) //nolint:errcheck
	if _, err := tmp.Write(req.GetContent()); err != nil {
		_ = tmp.Close()
		return &pb.PutFileReply{Error: err.Error()}, nil
	}
	if err := tmp.Close(); err != nil {
		return &pb.PutFileReply{Error: err.Error()}, nil
	}
	err = s.exec.PutFile(ctx, tmpPath, req.GetPath(), req.GetMode(), req.GetOwnerRoot(), decodeReverseEmitOpts(req.GetOptsJson()))
	return &pb.PutFileReply{Error: errString(err)}, nil
}

// RunCapture is the CHECK-VERB capture leg: an out-of-process exec-based check verb
// (record/dbus — and wl when it externalizes) probes the live venue by capturing
// stdout/stderr/exit. No root
// escalation — the verb's script adds sudo if it needs it. The gRPC call itself
// succeeds; an execution failure (not a non-zero exit) travels in CaptureReply.Error.
func (s *executorReverseServer) RunCapture(ctx context.Context, req *pb.RunRequest) (*pb.CaptureReply, error) {
	stdout, stderr, exit, err := s.exec.RunCapture(ctx, req.GetScript())
	return &pb.CaptureReply{Stdout: stdout, Stderr: stderr, ExitCode: int32(exit), Error: errString(err)}, nil
}

// RunInteractive is the F12 LIVE-TTY leg: this server runs IN the charly host process, so
// s.exec.RunInteractive inherits the host's os.Stdin/os.Stdout/os.Stderr = the OPERATOR's
// terminal. Stdio never crosses the wire — only the script came in and the exit code goes back.
// The pod plugin's OpAttach drives it for `charly shell`/`charly cmd`. Not deadlined (the TTY owns
// its lifetime).
func (s *executorReverseServer) RunInteractive(ctx context.Context, req *pb.RunRequest) (*pb.LiveReply, error) {
	exit, err := s.exec.RunInteractive(ctx, req.GetScript())
	return &pb.LiveReply{ExitCode: int32(exit), Error: errString(err)}, nil
}

// RunStream is the F12 LIVE-OUTPUT leg (charly logs --follow): streams stdout/stderr to the
// operator's terminal (host-held); only script→exit crosses the wire.
func (s *executorReverseServer) RunStream(ctx context.Context, req *pb.RunRequest) (*pb.LiveReply, error) {
	exit, err := s.exec.RunStream(ctx, req.GetScript())
	return &pb.LiveReply{ExitCode: int32(exit), Error: errString(err)}, nil
}

// GetFile is the CHECK-VERB artifact-pull leg: a verb that produces a file on the venue
// (a record .cast / a screenshot) reads it back to the host. asRoot reads via sudo.
func (s *executorReverseServer) GetFile(ctx context.Context, req *pb.GetFileRequest) (*pb.GetFileReply, error) {
	content, err := s.exec.GetFile(ctx, req.GetPath(), req.GetAsRoot(), decodeReverseEmitOpts(req.GetOptsJson()))
	return &pb.GetFileReply{Content: content, Error: errString(err)}, nil
}

// RunHostStep is the HOST-ENGINE channel leg (the generalization of the former F3 build channel): an
// OUT-OF-PROCESS deploy/step plugin walking an InstallPlan hits one of the six step kinds
// it CANNOT execute itself because each needs in-core host machinery that cannot move into
// the leaf plugin/kit package — BuilderStep (podman / BuilderRun / EnsureImagePresent),
// LocalPkgInstallStep (makepkg + pacman/dnf/apt), SystemPackagesStep (the format's
// phase.install.host template, rendered from the project DistroConfig), an act-verb OpStep
// (a builtin ProvisionActor shell that needs the in-proc provider registry), an
// ExternalPluginStep (a verb served by ANOTHER out-of-process plugin, dispatched over a
// NESTED reverse channel), or a RebootStep (reboot a charly-owned VM guest + wait for the
// deterministic boot_id change — only on a rebootable venue; skip-and-noted on a host venue).
// The plugin dials back here; the host reconstructs the concrete
// step from its serializable view, runs the EXISTING in-core machinery on the host (the
// SAME helpers the in-proc deploy targets use — no second implementation, R3), applies the
// effect onto the venue via s.exec, and returns the step's recorded ReverseOps so the
// plugin folds them into its DeployReply (record-and-replay teardown). Every OTHER
// (plugin-renderable) kind the plugin EXECUTES itself via the RunSystem/RunUser/PutFile
// legs — reaching RunHostStep with one is a plugin-walk bug (loud error). The plugin owns
// the plan WALK ordering; the host owns the host ENGINE. A host-engine/apply failure rides
// the reply's error field (the RPC itself succeeds, like runReply).
func (s *executorReverseServer) RunHostStep(ctx context.Context, req *pb.HostStepRequest) (*pb.HostStepReply, error) {
	var view spec.InstallStepView
	if err := json.Unmarshal(req.GetStepJson(), &view); err != nil {
		return &pb.HostStepReply{Error: fmt.Sprintf("decode step view: %v", err)}, nil
	}
	step, err := deploykit.StepFromView(view)
	if err != nil {
		return &pb.HostStepReply{Error: err.Error()}, nil
	}
	opts := decodeReverseEmitOpts(req.GetOptsJson())

	var reverseOps []spec.ReverseOp
	switch st := step.(type) {
	case *deploykit.BuilderStep:
		venueHome, herr := s.exec.ResolveHome(ctx, "")
		if herr != nil {
			return &pb.HostStepReply{Error: fmt.Sprintf("resolve venue home: %v", herr)}, nil
		}
		if rerr := runVenueBuilderStep(ctx, s.exec, venueHome, s.build, st, opts); rerr != nil {
			return &pb.HostStepReply{Error: rerr.Error()}, nil
		}
		reverseOps = st.Reverse()
	case *deploykit.LocalPkgInstallStep:
		supported := deploykit.VenueHasPkgManager(ctx, s.exec, st.LocalPkg, opts)
		if rerr := deploykit.ExecLocalPkgInstall(ctx, s.exec, st, supported, s.exec.Venue(), opts); rerr != nil {
			return &pb.HostStepReply{Error: rerr.Error()}, nil
		}
		reverseOps = st.Reverse()
	case *deploykit.SystemPackagesStep:
		// The format's phase.install.host template lives in the resolved DistroConfig the
		// plugin cannot reach — render it host-side (the SAME deploykit.RenderHostPackageCommand
		// the host-engine deploy paths use, R3) and RunSystem on the venue.
		cmd, rerr := deploykit.RenderHostPackageCommand(s.build.DistroCfg, st)
		if rerr != nil {
			return &pb.HostStepReply{Error: rerr.Error()}, nil
		}
		if cmd != "" { // empty = no host render for this phase (a clean no-op, not an error)
			if rerr := s.exec.RunSystem(ctx, cmd, opts); rerr != nil {
				return &pb.HostStepReply{Error: fmt.Sprintf("system packages %s: %v", st.Format, rerr)}, nil
			}
		}
		reverseOps = st.Reverse()
	case *deploykit.OpStep:
		// An act-verb OpStep (a `run: plugin: <verb>` whose builtin ProvisionActor shell
		// needs the in-proc registry). resolveProvisionScript is the SAME Op→act-shell seam
		// the in-proc deploy path (renderOpCommand) uses (R3). A NON-act OpStep
		// (mkdir/copy/write/link/setcap/download/cmd/plugin:command) is plugin-renderable and
		// must NOT arrive here — ok=false is a loud plugin-walk bug.
		script, ok := resolveProvisionScript(st.Op, st.Distros)
		if !ok {
			return &pb.HostStepReply{Error: "RunHostStep: OpStep verb is not act-capable (a plugin-renderable OpStep must be executed by the plugin via RunSystem/RunUser, not routed to RunHostStep)"}, nil
		}
		runErr := s.exec.RunUser(ctx, script, opts)
		if st.Scope() == spec.ScopeSystem {
			runErr = s.exec.RunSystem(ctx, script, opts)
		}
		if runErr != nil {
			return &pb.HostStepReply{Error: runErr.Error()}, nil
		}
		reverseOps = st.Reverse()
	case *deploykit.ExternalPluginStep:
		// A verb served by ANOTHER out-of-process plugin — the host stands up a SECOND
		// reverse channel on THAT plugin's broker (a nested reverse channel, delegating to
		// the SAME venue executor s.exec) and Invokes its OpExecute, via the SAME
		// PLUGIN↔PLUGIN InvokeProvider leg (S4, R3) every other peer-invoke uses. The nested
		// plugin's teardown ReverseOps ride the reply.
		params, perr := marshalJSON(st.Op.PluginInput)
		if perr != nil {
			return &pb.HostStepReply{Error: fmt.Sprintf("external plugin step %q: marshal plugin_input: %v", st.Op.Plugin, perr)}, nil
		}
		reply, rerr := s.invokeExternalStep(ctx, ClassVerb, st.Op.Plugin, params)
		if rerr != nil {
			return &pb.HostStepReply{Error: rerr.Error()}, nil
		}
		reverseOps = reply.ReverseOps
	case *deploykit.RebootStep:
		// A `reboot: true` layer. ONLY a rebootable venue (a VM guest — s.rebootable, set by
		// the vm deploy substrate) is rebooted: the host records the guest boot_id, fires the
		// reboot, and polls until sshd answers AND the boot_id changed (deterministic, not a
		// sleep — the SSHExecutor dials fresh each poll, so the post-reboot reconnect is
		// automatic). On a NON-rebootable venue (target:local host:local/remote) it skips +
		// notes — a plugin walk NEVER reboots the operator/remote host (the prior in-proc
		// LocalDeployTarget behaviour). RebootStep.Reverse() is empty either way.
		if !s.rebootable {
			fmt.Fprintf(os.Stderr, "charly: reboot requested by candy %q skipped on host venue (a plugin walk never reboots a non-VM venue)\n", st.CandyName)
			reverseOps = st.Reverse()
			break
		}
		if rerr := rebootVenueAndWait(ctx, s.exec, st.CandyName, opts); rerr != nil {
			return &pb.HostStepReply{Error: rerr.Error()}, nil
		}
		reverseOps = st.Reverse()
	case *deploykit.ExternalStep:
		// An EXTERNAL (plugin-contributed) step kind (F3): "external:<word>". The host
		// dispatches it to its serving class:step plugin's OpExecute over a nested reverse
		// channel (delegating to the SAME venue executor s.exec) — the generalization of the
		// ExternalPlugin arm above via the SAME shared invokeExternalStep dispatch (S4, R3),
		// but the provider is resolved by ClassStep and the params are the opaque Payload
		// verbatim. The plugin's dynamic teardown ReverseOps ride the reply (record-and-replay).
		reply, rerr := s.invokeExternalStep(ctx, ClassStep, st.Word, st.Payload)
		if rerr != nil {
			return &pb.HostStepReply{Error: rerr.Error()}, nil
		}
		reverseOps = reply.ReverseOps
	default:
		// Only the six host-engine step kinds route here. Every other (plugin-renderable)
		// kind the plugin EXECUTES itself via the RunSystem/RunUser/PutFile legs — reaching
		// RunHostStep with one is a plugin-walk bug.
		return &pb.HostStepReply{Error: fmt.Sprintf("RunHostStep: step kind %q is not a host-engine step (only Builder / LocalPkgInstall / SystemPackages / act-verb Op / ExternalPlugin / Reboot route through the host-engine channel; execute every other kind via RunSystem/RunUser/PutFile)", view.Kind)}, nil
	}

	revJSON, err := json.Marshal(reverseOps)
	if err != nil {
		return &pb.HostStepReply{Error: fmt.Sprintf("marshal reverse ops: %v", err)}, nil
	}
	return &pb.HostStepReply{ReverseOpsJson: revJSON}, nil
}

// invokeExternalStep is the shared OpExecute dispatch RunHostStep's ExternalPluginStep and
// ExternalStep arms both use (S4, R3): it is the RunHostStep-local counterpart of the generic
// PLUGIN↔PLUGIN InvokeProvider leg (plugin_dispatch_reverse.go) — literally calls it, since
// RunHostStep already runs as a method on the SAME executorReverseServer InvokeProvider is
// served from, so no wire hop is needed. params carries the opaque OpExecute payload (a
// verb-step's marshalled plugin_input, or an external step kind's Payload verbatim); the venue
// descriptor is always the zero spec.DeployVenue here (RunHostStep never threads a plan — the
// verb/step's own params already carry whatever scratch-location identity it needs). Decodes
// the reply into spec.DeployReply so the caller can fold its dynamic ReverseOps.
func (s *executorReverseServer) invokeExternalStep(ctx context.Context, class ProviderClass, word string, params []byte) (spec.DeployReply, error) {
	var zero spec.DeployReply
	env, err := marshalJSON(spec.DeployVenue{})
	if err != nil {
		return zero, fmt.Errorf("external step %q: marshal venue: %w", word, err)
	}
	res, err := s.InvokeProvider(ctx, &pb.InvokeProviderRequest{
		Class:      string(class),
		Reserved:   word,
		Op:         OpExecute,
		ParamsJson: params,
		EnvJson:    env,
	})
	if err != nil {
		return zero, err
	}
	var reply spec.DeployReply
	if res != nil && len(res.GetResultJson()) > 0 {
		if err := json.Unmarshal(res.GetResultJson(), &reply); err != nil {
			return zero, fmt.Errorf("external step %q: decode reply: %w", word, err)
		}
	}
	return reply, nil
}

// rebootVenueAndWait reboots a charly-owned guest over the executor and waits for it to
// return. Deterministic, not a sleep-and-pray: it records the kernel boot_id, fires the
// reboot in the background (so the ssh session closes cleanly), then polls until the
// executor answers again AND the boot_id has changed — so the still-up pre-reboot sshd
// can't be mistaken for "back up". Needed by kernel-module candies (e.g. nvidia-open-dkms)
// whose module only loads on a fresh boot. Relocated from the deleted in-proc VM-target reboot leg
// (the SAME readiness primitive + boot_id gate), now driven host-side over the reverse channel
// so the external vm plugin's walk reboots the guest mid-plan. The SSHExecutor dials fresh per
// call, so the post-reboot reconnect is automatic.
func rebootVenueAndWait(ctx context.Context, exec deploykit.DeployExecutor, candyName string, opts deploykit.EmitOpts) error {
	if opts.DryRun {
		fmt.Fprintf(os.Stderr, "[dry-run] reboot guest (candy %s) and wait for it to return\n", candyName)
		return nil
	}
	venue := exec.Venue()
	oldBoot, _, _, _ := exec.RunCapture(ctx, "cat /proc/sys/kernel/random/boot_id 2>/dev/null")
	oldBoot = strings.TrimSpace(oldBoot)

	fmt.Fprintf(os.Stderr, "reboot: requested by candy %q — rebooting guest %s and waiting for it to return\n", candyName, venue)
	// Fire the reboot in the background so the ssh session closes cleanly (a foreground
	// `reboot` would race the connection teardown and yield an ambiguous exit code). The 1s
	// delay is for clean session close, not a correctness-timing workaround.
	_ = exec.RunSystem(ctx, "(sleep 1; systemctl reboot || reboot) >/dev/null 2>&1 &\nexit 0", opts)

	// BINARY/EDGE readiness (guest down→boot_id-changed) → cap-only via pollUntil (poll.go)
	// at the GENEROUS config cap. The marker is frozen "down" for the whole legitimate reboot,
	// so a no-progress window would be a wrong (too-short) timeout — cap-only is correct here.
	cfg := loadedReadiness().WaitCapped(fmt.Sprintf("reboot %s", venue), PollRemote, 0)
	if err := pollUntil(ctx, cfg, func(actx context.Context) (bool, float64, error) {
		out, _, _, rerr := exec.RunCapture(actx, "cat /proc/sys/kernel/random/boot_id 2>/dev/null")
		if rerr != nil {
			return false, 0, nil // guest still down or sshd not yet accepting
		}
		newBoot := strings.TrimSpace(out)
		if newBoot == "" {
			return false, 0, nil
		}
		if oldBoot == "" || newBoot != oldBoot {
			fmt.Fprintf(os.Stderr, "reboot: guest %s is back up (boot_id=%s)\n", venue, newBoot)
			return true, 0, nil
		}
		return false, 0, nil // sshd back but still the pre-reboot kernel
	}); err != nil {
		return fmt.Errorf("guest %s did not return after reboot requested by candy %q: %w", venue, candyName, err)
	}
	return nil
}

// errString is err.Error() or "" — the reverse-channel convention (the RPC succeeds; the
// venue-op failure rides the reply's error field, like runReply).
func errString(err error) string {
	if err != nil {
		return err.Error()
	}
	return ""
}

// decodeReverseEmitOpts decodes the JSON EmitOpts carried in a RunRequest; an empty
// payload yields the zero EmitOpts (the common "no options" call).
func decodeReverseEmitOpts(b []byte) deploykit.EmitOpts {
	var o deploykit.EmitOpts
	if len(b) > 0 {
		_ = json.Unmarshal(b, &o)
	}
	return o
}

// runReply maps a DeployExecutor error onto a RunReply — the gRPC call itself
// succeeds; the script's error (if any) travels in the reply so the plugin sees the
// same string the in-proc executor would have returned.
func runReply(err error) (*pb.RunReply, error) {
	if err != nil {
		return &pb.RunReply{Error: err.Error()}, nil
	}
	return &pb.RunReply{}, nil
}
