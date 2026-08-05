package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/opencharly/spec/exitcode"
	"github.com/opencharly/spec/ops"
	"github.com/opencharly/spec/spec"
)

// host_build_pod_lifecycle_dispatch.go — the CONSOLIDATED "pod-lifecycle" F10 host-builder (Cutover
// B unit 2, the pod-lifecycle-CLI-dispatch family; #55 W3 A10 folded start/stop's former
// pod_lifecycle_verb.go wrappers in here (A10a), then #55 W3 A10b unified the former 8-per-verb
// "pod-{start,stop,shell,cmd,logs,update,service,remove}" HostBuild kinds into this ONE
// op-discriminated handler behind ONE registered kind, converging the seam on the codebase's own
// established idiom (#ArbiterInvokeInput's flat action-multiplexed shape; charly/provider.go's own
// Operation.Params json.RawMessage envelope) — spec.PodLifecycleRequest carries op+box+instance+
// node (the shared envelope) plus an opaque payload the switch below re-decodes into the matching
// #PodXPayload once op is known, exactly like every OTHER Invoke op on the wire already works.
//
// SIX of the eight ops — start/stop/shell/logs/service/cmd — share ONE literal shape: resolve the
// deploy's LifecycleTarget via dispatchLifecycleTarget (pod_lifecycle_verb.go: ResolveTarget + the
// plugin loader + the live-executor composition, a core (M) Mechanism per the kernel/plugin
// boundary law — the ONE step that cannot cross the plugin boundary), then run exactly one
// LifecycleTarget method against it. dispatchAndRunLifecycle is that shared core — every case below
// is just the per-op payload-decode → closure translation, not a second copy of the
// resolve+error-check skeleton. Every OTHER piece of the former podStartCmd/podStopCmd/
// podShellCmd/podLogsCmd/podServiceCmd orchestration (remote-ref validation,
// CanonicalizeDeployArg, resolveServiceInit/validateServiceName/argv-rendering) MOVED to
// candy/plugin-pod's pod_cmd.go + service_resolve.go — the plugin now performs those checks
// itself before calling this seam, mirroring the P13-KERNEL deploy-node-dispatch precedent at
// single-node granularity (no ancestor-descriptor list is needed: dispatchLifecycleTarget resolves
// exactly ONE deploy-config entry, never a tree).
//
// update and remove do NOT share the LifecycleTarget shape and are NOT forced into it: update
// keeps its existing podUpdateCmd/dispatchByDeployTarget body (update_deploy_dispatch.go)
// UNCHANGED — that resolver is registry+loader-coupled the same way, just via the project tree
// instead of the per-host deploy config, never touching a LifecycleTarget at all. remove is FULLY
// ported to candy/plugin-pod's RemoveCmd.Run() (remove_orchestration.go + remove_tunnel.go); all
// that remains under op="remove" is the arbiter-release bracket, host-process
// CHARLY_PREEMPT_LEASE state a placement-agnostic plugin cannot own. Forcing these two through
// dispatchAndRunLifecycle would trade a real shape mismatch for false uniformity (R3 only applies
// to code that is ACTUALLY duplicated) — they stay their own switch cases, calling their own
// existing bodies directly.
//
// Interactive/streaming safety (RDD claim, closed both at design level and on a live disposable
// bed — check-sidecar-pod, 12/12 steps PASS including the fresh `charly update` gate): this
// property is PLACEMENT-INVARIANT, not contingent on candy/plugin-pod's compiled-in-vs-out-of-
// process placement (that per-BUILD choice is never an authoring assumption). This handler is
// HostBuild-seam code, which by construction ALWAYS executes in the charly host process
// regardless of the CALLING plugin's placement — so isTerminal()/tty detection computed HERE sees
// the operator's REAL terminal unconditionally. The actual interactive/streaming subprocess spawn
// (`podman exec -it`/`podman logs -f`, wired to real os.Stdin/os.Stdout) is itself architecturally
// pinned to the host process too: candy/plugin-deploy-pod's podAttach/podLogs (lifecycle.go) call
// the *specexec.Executor RPC-client stub of exec.RunInteractive/RunStream, which always lands on
// charly/plugin_executor_reverse.go's executorReverseServer — living ONLY in the charly binary,
// bridged identically whether the calling plugin is compiled-in (plugin_inproc_reverse.go, a direct
// Go call) or out-of-process (specexec.ExecutorFromInvoke's go-plugin GRPCBroker dial). Proven, not
// hypothetical: candy/plugin-deploy-pod is NOT in charly.yml's compiled_plugins: today — it already
// ships out-of-process by default, and `charly shell`/`cmd`/`logs -f` already work against it in
// production on exactly this mechanism.

// --- the arbiter-release bracket (folded from the deleted preempt.go, K-wave 2 cone
// CONTESTED) ----------------------------------------------------------------------
//
// The op="remove" arbiter-release bracket is the ONE remaining core-side arbiter interaction:
// candy/plugin-pod's RemoveCmd.Run() defers this HostBuild op="remove" as its LAST step,
// reproducing the former core `defer releaseResourceClaim(...)`'s "always runs, after everything
// else" semantics. The release MUST stay host-side: CHARLY_PREEMPT_LEASE is host-process env
// state a placement-agnostic plugin cannot own (candy/plugin-pod ships out-of-process by
// default, so its own process env cannot see the outer orchestrator's lease guard). Every OTHER
// former arbiter consumer is peer-dispatch now — candy/plugin-check's bed_session, candy/plugin-vm's
// vm_arbiter_shim, and candy/plugin-bundle's handleLifecycleSimple all Invoke verb:arbiter
// directly (the compiled-in placement class bed_session.go documents) — so this release chain is
// the surviving in-core proxy, exercising the generic core→verb registry bridge.

// envPreemptLeaseHeld is set by the OUTERMOST claim-bringing `charly` invocation (a check-bed
// run, or a standalone `charly vm create`/`charly start`) so the nested `charly` subprocesses it
// spawns do NOT independently acquire/release the lease — the owner manages it.
const envPreemptLeaseHeld = "CHARLY_PREEMPT_LEASE"

// arbiterProxy is the in-core handle newResourceArbiter() returns to releaseResourceClaim. Its
// methods dispatch to the compiled-in candy/plugin-preempt (verb:arbiter) over an in-proc
// reverse channel.
type arbiterProxy struct{}

func newResourceArbiter() *arbiterProxy { return &arbiterProxy{} }

// arbiterInvoke resolves verb:arbiter and Invokes it with an action-tagged input, threading the
// IN-PROC reverse channel onto the ctx so the plugin's Invoke reaches its host seams over
// InvokeProvider/HostBuild (always-served generic seams — plugin_executor_reverse.go). Infra
// failures are returned as a Go error; a per-action OP failure rides reply.Error. This is the
// generic core→verb registry bridge (core is not a plugin, so it cannot call InvokeProvider).
func arbiterInvoke(in spec.ArbiterInvokeInput) (spec.ArbiterInvokeReply, error) {
	prov, ok := providerRegistry.resolve(ClassVerb, "arbiter")
	if !ok {
		return spec.ArbiterInvokeReply{}, fmt.Errorf("resource arbiter (verb:arbiter) not registered — charly built without candy/plugin-preempt")
	}
	ctx := hostInProcCtx()
	reply, err := invokeTyped[spec.ArbiterInvokeInput, spec.ArbiterInvokeReply](ctx, prov, "arbiter", ops.OpRun, in)
	if err != nil {
		return spec.ArbiterInvokeReply{}, fmt.Errorf("arbiter %s: %w", in.Action, err)
	}
	return reply, nil
}

// ReleaseClaimant restores the holders a claimant's lease stopped + removes the lease.
func (a *arbiterProxy) ReleaseClaimant(claimant string, success bool) error {
	r, err := arbiterInvoke(spec.ArbiterInvokeInput{Action: spec.ArbiterActionRelease, Claimant: claimant, Success: success})
	if err != nil {
		return err
	}
	if r.Error != "" {
		return errors.New(r.Error)
	}
	return nil
}

// releaseResourceClaim releases a persistent claimant's lease on teardown — kind-agnostic,
// best-effort, a no-op when the claimant holds no lease, skipped when an outer orchestrator
// owns the lease.
func releaseResourceClaim(claimant string) {
	if os.Getenv(envPreemptLeaseHeld) != "" {
		return
	}
	if err := newResourceArbiter().ReleaseClaimant(claimant, true); err != nil {
		fmt.Fprintf(os.Stderr, "preempt: %v\n", err)
	}
}

// dispatchAndRunLifecycle resolves node/box/instance's LifecycleTarget for op (the shared
// dispatchLifecycleTarget core-M step) and, on success, runs the caller's op-specific body against
// it — the shared core every start/stop/shell/logs/service/cmd case below delegates to.
func dispatchAndRunLifecycle(op string, node *spec.BundleNode, box, instance string, run func(spec.LifecycleTarget) error) error {
	lt, err := dispatchLifecycleTarget(op, node, spec.DeployKey(box, instance))
	if err != nil {
		return err
	}
	return run(lt)
}

// decodeLifecyclePayload decodes req.Payload into T (one of the #PodXPayload types), wrapping any
// error with the op name — the shared decode step every switch case below performs once payload's
// shape is known from req.Op (R3: one error-message format, not seven copies).
func decodeLifecyclePayload[T any](op string, payload []byte) (T, error) {
	var v T
	if err := json.Unmarshal(payload, &v); err != nil {
		return v, fmt.Errorf("pod-lifecycle %s: decode payload: %w", op, err)
	}
	return v, nil
}

// hostBuildPodLifecycle is the ONE op-discriminated handler behind the "pod-lifecycle" HostBuild
// kind (#55 W3 A10b). It decodes req.Payload into the #PodXPayload matching req.Op only once op is
// known — mirrors typedHostBuilder's own outer decode-then-run shape one level down, exactly like
// the plugin wire protocol's Operation.Params json.RawMessage.
func hostBuildPodLifecycle(_ context.Context, req spec.PodLifecycleRequest, _ buildEngineContext) (spec.PodLifecycleReply, error) {
	switch req.Op {
	case "start":
		p, perr := decodeLifecyclePayload[spec.PodStartPayload](req.Op, req.Payload)
		if perr != nil {
			return spec.PodLifecycleReply{}, perr
		}
		opts := podStartOpts{
			Env: p.Env, EnvFile: p.EnvFile, Port: p.Port, VolumeFlag: p.VolumeFlag,
			Bind: p.Bind, NoAutoDetect: p.NoAutoDetect,
		}
		err := dispatchAndRunLifecycle("start", req.Node, req.Box, req.Instance, func(lt spec.LifecycleTarget) error {
			return lt.Start(withPodStartOpts(context.Background(), opts))
		})
		return spec.PodLifecycleReply{}, err

	case "stop":
		p, perr := decodeLifecyclePayload[spec.PodStopPayload](req.Op, req.Payload)
		if perr != nil {
			return spec.PodLifecycleReply{}, perr
		}
		err := dispatchAndRunLifecycle("stop", req.Node, req.Box, req.Instance, func(lt spec.LifecycleTarget) error {
			return lt.Stop(withPodStopUnmount(context.Background(), p.Unmount))
		})
		return spec.PodLifecycleReply{}, err

	case "shell":
		p, perr := decodeLifecyclePayload[spec.PodShellPayload](req.Op, req.Payload)
		if perr != nil {
			return spec.PodLifecycleReply{}, perr
		}
		opts := podShellOpts{
			Tag: p.Tag, EnvFile: p.EnvFile, Env: p.Env, VolumeFlag: p.VolumeFlag,
			Bind: p.Bind, NoAutoDetect: p.NoAutoDetect,
			// HOST-resolved NOW against the REAL terminal — see the file header invariant.
			Interactive: p.TTY || isTerminal(),
			WrapPTY:     p.TTY && !isTerminal(),
		}
		var cmd []string
		if p.Command != "" {
			cmd = []string{p.Command}
		}
		err := dispatchAndRunLifecycle("shell", req.Node, req.Box, req.Instance, func(lt spec.LifecycleTarget) error {
			return lt.Attach(withPodShellOpts(context.Background(), opts), cmd, true)
		})
		return spec.PodLifecycleReply{}, err

	case "logs":
		p, perr := decodeLifecyclePayload[spec.PodLogsPayload](req.Op, req.Payload)
		if perr != nil {
			return spec.PodLifecycleReply{}, perr
		}
		err := dispatchAndRunLifecycle("logs", req.Node, req.Box, req.Instance, func(lt spec.LifecycleTarget) error {
			return lt.Logs(context.Background(), spec.DeployTargetLogsOpts{Follow: p.Follow, Sidecar: p.Sidecar})
		})
		return spec.PodLifecycleReply{}, err

	case "service":
		// service is now FULLY ported (Cutover B unit 2 completion): candy/plugin-pod's
		// buildServiceArgv resolves + validates + renders the FULL argv itself (all portable —
		// see service_resolve.go), so this case does ONLY the irreducible dispatchLifecycleTarget
		// + LifecycleTarget.Shell step, exactly like start/stop/logs/cmd above.
		p, perr := decodeLifecyclePayload[spec.PodServicePayload](req.Op, req.Payload)
		if perr != nil {
			return spec.PodLifecycleReply{}, perr
		}
		err := dispatchAndRunLifecycle("service", req.Node, req.Box, req.Instance, func(lt spec.LifecycleTarget) error {
			return lt.Shell(context.Background(), p.Argv)
		})
		return spec.PodLifecycleReply{}, err

	case "cmd":
		// The container command's non-zero exit rides the REPLY's ExitCode field, NOT the HostBuild
		// error return (which stringifies the typed *exitcode.ExitCodeError, losing the code) — the
		// reply is reconstructed from it so the operator sees the command's own code, exactly as the
		// former __cmd/CliReply.ExitCode path did. A genuine (non-exit-code) failure still propagates
		// as the error.
		p, perr := decodeLifecyclePayload[spec.PodCmdPayload](req.Op, req.Payload)
		if perr != nil {
			return spec.PodLifecycleReply{}, perr
		}
		err := dispatchAndRunLifecycle("cmd", req.Node, req.Box, req.Instance, func(lt spec.LifecycleTarget) error {
			return lt.Attach(withPodCmdOpts(context.Background(), podCmdOpts{Sidecar: p.Sidecar}), []string{p.Command}, false)
		})
		var ece *exitcode.ExitCodeError
		if errors.As(err, &ece) {
			return spec.PodLifecycleReply{ExitCode: ece.Code}, nil
		}
		return spec.PodLifecycleReply{}, err

	case "update":
		p, perr := decodeLifecyclePayload[spec.PodUpdatePayload](req.Op, req.Payload)
		if perr != nil {
			return spec.PodLifecycleReply{}, perr
		}
		cmd := podUpdateCmd{
			Box: req.Box, Tag: p.Tag, Build: p.Build, Instance: req.Instance,
			Seed: p.Seed, ForceSeed: p.ForceSeed, DataFrom: p.DataFrom,
			TreeJSON: p.TreeJSON,
		}
		return spec.PodLifecycleReply{}, cmd.dispatchByDeployTarget()

	case "remove":
		// remove is FULLY reduced (Cutover B unit 2 remove-verb completion, option (b)):
		// candy/plugin-pod's RemoveCmd.Run() owns the ENTIRE orchestration itself
		// (remove_orchestration.go + remove_tunnel.go); the former host-coupled axes
		// (pod-config-hook-secret-env, pod-config-clean-deploy-entry) are BOTH retired — the
		// credential env resolves plugin-side and the deploy-entry cleanup runs plugin-side via
		// deploykit.CleanDeployEntry. All that remains under op="remove" is the
		// arbiter-release bracket (releaseResourceClaim, folded here from the deleted
		// preempt.go) — CHARLY_PREEMPT_LEASE-gated host-process state a placement-agnostic
		// plugin cannot own (candy/plugin-pod ships out-of-process, so its own env cannot see
		// the outer orchestrator's guard). This is now the ONLY core-side arbiter bracket: the
		// deploy-dispatch Start/Stop bracket went peer-dispatch at K-wave 2 cone R2 bank E
		// (candy/plugin-bundle's handleLifecycleSimple Invokes verb:arbiter directly; the
		// "arbiter-bracket-*" HostBuild seam is DELETED). The plugin defers this call as its
		// LAST step, reproducing the former core `defer releaseResourceClaim(...)`'s "always
		// runs, after everything else" semantics. It does NOT touch a LifecycleTarget, so it
		// stays outside dispatchAndRunLifecycle (see file header).
		releaseResourceClaim(spec.DeployKey(req.Box, req.Instance))
		return spec.PodLifecycleReply{}, nil

	default:
		return spec.PodLifecycleReply{}, fmt.Errorf("pod-lifecycle: unknown op %q", req.Op)
	}
}

var _ = func() bool {
	registerHostBuilder("pod-lifecycle", typedHostBuilder("pod-lifecycle", hostBuildPodLifecycle))
	return true
}()
