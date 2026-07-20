package deploypod

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk/spec"
)

// lifecycle.go — the host-side POD venue lifecycle, externalized out of charly core (M4). The
// generic grpcSubstrateLifecycle proxy Invokes these Ops over the reverse channel; the plugin runs
// ON the host but out-of-process, so it reaches the overlay BUILD engine (kept core) via
// HostBuild("overlay") and drives the container lifecycle via HostBuild("cli") — never in-process
// podman/Generator. PrepareVenue builds the overlay + returns a host-local shell venue; Start/Stop/
// Status/Logs/Shell/Rebuild shell to the charly CLI; PostTeardown = `charly remove` + drop overlay.

// lifecycleParams are the common params the host proxy ships for a pod lifecycle Op (image/version
// are passed explicitly so the plugin need not decode the whole BundleNode). Opts is polymorphic
// (LifecycleOpts for PrepareVenue, LogsOpts for Logs, RebuildOpts for Rebuild) — decoded per-op.
type lifecycleParams struct {
	Name      string          `json:"name"`
	Dir       string          `json:"dir"`
	Image     string          `json:"image"`
	Version   string          `json:"version"`
	Opts      json.RawMessage `json:"opts"`
	KeepImage bool            `json:"keep_image"`
	EngineBin string          `json:"engine_bin"` // host-resolved container engine binary (PostTeardown)
	Cmd       []string        `json:"cmd"`
	Plan      json.RawMessage `json:"plan"` // host-resolved spec.PodLifecyclePlan (OpStart/OpStop) — the K4 deep-body move
}

// isLifecycleOp reports whether op is a substrate-lifecycle Op (vs. the OpExecute deploy walk).
func isLifecycleOp(op string) bool {
	switch op {
	case sdk.OpPrepareVenue, sdk.OpArtifactKey, sdk.OpPostApply, sdk.OpTeardownExecutor,
		sdk.OpPostTeardown, sdk.OpStart, sdk.OpStop, sdk.OpStatus, sdk.OpLogs, sdk.OpShell,
		sdk.OpAttach, sdk.OpRebuild:
		return true
	}
	return false
}

// invokeLifecycle handles a pod substrate-lifecycle Op over the reverse channel.
func invokeLifecycle(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	exec, err := sdk.ExecutorFromInvoke(req.GetExecutorBrokerId())
	if err != nil {
		return nil, fmt.Errorf("plugin-deploy-pod %s: executor: %w", req.GetOp(), err)
	}
	var p lifecycleParams
	_ = json.Unmarshal(req.GetParamsJson(), &p)

	switch req.GetOp() {
	case sdk.OpPrepareVenue:
		return podPrepareVenue(ctx, exec, p)
	case sdk.OpStart:
		return podStart(ctx, exec, p)
	case sdk.OpStop:
		return podStop(ctx, exec, p)
	case sdk.OpStatus:
		return podStatus(ctx, exec, p.Name)
	case sdk.OpLogs:
		return podLogs(ctx, exec, p)
	case sdk.OpShell:
		return podExec(ctx, exec, p)
	case sdk.OpAttach:
		return podAttach(ctx, exec, p)
	case sdk.OpRebuild:
		return podRebuild(ctx, exec, p)
	case sdk.OpPostTeardown:
		return podPostTeardown(ctx, exec, p)
	case sdk.OpArtifactKey:
		return marshalReply(map[string]string{"key": ""})
	case sdk.OpPostApply, sdk.OpTeardownExecutor:
		return marshalReply(struct{}{}) // pod: no-op / caller keeps its executor
	}
	return nil, fmt.Errorf("plugin-deploy-pod: unhandled lifecycle op %q", req.GetOp())
}

// podCli requests the host run `charly <argv>` via the "cli" host-builder.
func podCli(ctx context.Context, exec *sdk.Executor, capture, bestEffort bool, argv ...string) (spec.CliReply, error) {
	reqJSON, err := json.Marshal(spec.CliRequest{Argv: argv, Capture: capture, BestEffort: bestEffort})
	if err != nil {
		return spec.CliReply{}, err
	}
	resJSON, err := exec.HostBuild(ctx, "cli", reqJSON)
	if err != nil {
		return spec.CliReply{}, err
	}
	var r spec.CliReply
	if uerr := json.Unmarshal(resJSON, &r); uerr != nil {
		return spec.CliReply{}, uerr
	}
	if r.Error != "" {
		return r, fmt.Errorf("charly %s: %s", strings.Join(argv, " "), r.Error)
	}
	return r, nil
}

// podStart executes the host-resolved pod START plan (the K4 deep-body move — the former
// podCli("start") `charly start` reentry, now a real in-plugin body): mount encrypted volumes
// (InvokeProvider verb:enc, BEFORE the container so it sees the plaintext), start the container
// (systemctl/podman over the served host executor), then start the tunnel (InvokeProvider
// verb:tunnel, AFTER — best-effort, matching StartCmd). The host RESOLVED the plan (#59 inventory:
// image/metadata/overlay/volumes/env/security/ports/network/buildStartArgs + the enc/tunnel inputs)
// and BRACKETED the arbiter claim around this op (acquire before, release after/on-failure).
func podStart(ctx context.Context, exec *sdk.Executor, p lifecycleParams) (*pb.InvokeReply, error) {
	var opts spec.PodStartOpts
	if err := json.Unmarshal(p.Plan, &opts); err != nil {
		return nil, fmt.Errorf("plugin-deploy-pod start: decode opts: %w", err)
	}
	box, instance := deploykit.ParseDeployKey(p.Name)
	planPtr, err := resolvePodStartPlan(ctx, exec, box, instance, opts)
	if err != nil {
		return nil, fmt.Errorf("plugin-deploy-pod start: resolve plan: %w", err)
	}
	plan := *planPtr
	if len(plan.Enc) > 0 {
		if _, err := exec.InvokeProvider(ctx, "verb", "enc", sdk.OpExecute, plan.Enc, nil); err != nil {
			return nil, fmt.Errorf("plugin-deploy-pod start: mount encrypted volumes: %w", err)
		}
	}
	if err := podContainerStart(ctx, exec, plan); err != nil {
		return nil, err
	}
	if plan.Tunnel != nil {
		if err := podTunnelOp(ctx, exec, "start", plan.Tunnel); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: tunnel setup failed: %v\n", err)
		}
	}
	return marshalReply(struct{}{})
}

// podStop executes the host-resolved pod STOP plan (the former podCli("stop") reentry): stop the
// tunnel first (matching StopCmd's tunnel-before-stop), stop the container (systemctl/engine), then
// unmount encrypted volumes if `--unmount` was requested. The arbiter release is bracketed
// host-side by the F6 dispatch (after this op + on the failure path).
func podStop(ctx context.Context, exec *sdk.Executor, p lifecycleParams) (*pb.InvokeReply, error) {
	var opts spec.PodStopOpts
	if err := json.Unmarshal(p.Plan, &opts); err != nil {
		return nil, fmt.Errorf("plugin-deploy-pod stop: decode opts: %w", err)
	}
	box, instance := deploykit.ParseDeployKey(p.Name)
	planPtr, err := resolvePodStopPlan(ctx, exec, box, instance, opts.Unmount)
	if err != nil {
		return nil, fmt.Errorf("plugin-deploy-pod stop: resolve plan: %w", err)
	}
	plan := *planPtr
	if plan.Tunnel != nil {
		if err := podTunnelOp(ctx, exec, "stop", plan.Tunnel); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: tunnel teardown failed: %v\n", err)
		}
	}
	if err := podContainerStop(ctx, exec, plan); err != nil {
		return nil, err
	}
	if len(plan.Enc) > 0 {
		if _, err := exec.InvokeProvider(ctx, "verb", "enc", sdk.OpExecute, plan.Enc, nil); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: encrypted-volume unmount failed: %v\n", err)
		}
	}
	return marshalReply(struct{}{})
}

// podExec runs a NON-interactive in-container command CAPTURED over the served executor (the K4
// `charly service` move — an in-container init-mgmt exec, `<engine> exec <ctr> <tool> <op> <svc>`,
// which the host resolved into p.Cmd). It returns the combined output + the EXACT exit code as a
// spec.PodExecReply so the host reprints the output (placement-agnostic — an out-of-process plugin's
// stdout is not charly's) and propagates the exit code exactly. Interactive `charly shell` stays a
// CORE command (a host-process TTY that cannot ride Invoke-and-reply — F12/#62), so it never reaches
// here; an empty argv is a defensive error.
func podExec(ctx context.Context, exec *sdk.Executor, p lifecycleParams) (*pb.InvokeReply, error) {
	if len(p.Cmd) == 0 {
		return nil, fmt.Errorf("plugin-deploy-pod shell: interactive shell is a core command (F12/#62), not an F6 op")
	}
	stdout, stderr, code, err := exec.RunCapture(ctx, shellJoin(p.Cmd))
	if err != nil && code == 0 {
		return nil, fmt.Errorf("plugin-deploy-pod exec %q: %w", strings.Join(p.Cmd, " "), err)
	}
	return marshalReply(spec.PodExecReply{Output: stdout + stderr, ExitCode: code})
}

// podContainerStart runs the resolved container start over the served host executor: quadlet →
// `systemctl --user start <svc>`; direct-deploy marker → `podman start <ctr>`; direct → the
// fully-built `podman run -d …` argv. Mirrors StartCmd.runQuadlet / runDirect's exec.
func podContainerStart(ctx context.Context, exec *sdk.Executor, plan spec.PodLifecyclePlan) error {
	switch {
	case plan.Mode == "direct":
		return execErr(exec, ctx, shellJoin(plan.RunArgv), "start (direct)", plan.ContainerName)
	case plan.DirectDeploy:
		return execErr(exec, ctx, "podman start "+kit.ShellQuote(plan.ContainerName), "start (direct-deploy)", plan.ContainerName)
	default:
		return execErr(exec, ctx, "systemctl --user start "+kit.ShellQuote(plan.SvcName), "start", plan.SvcName)
	}
}

// podContainerStop runs the resolved container stop: quadlet → `systemctl --user stop <svc>` (always
// via systemctl so podman-stop + Restart=always cannot restart-loop); direct → `<engine> stop <ctr>`.
func podContainerStop(ctx context.Context, exec *sdk.Executor, plan spec.PodLifecyclePlan) error {
	if plan.Mode == "quadlet" {
		return execErr(exec, ctx, "systemctl --user stop "+kit.ShellQuote(plan.SvcName), "stop", plan.SvcName)
	}
	return execErr(exec, ctx, plan.EngineBin+" stop "+kit.ShellQuote(plan.ContainerName), "stop", plan.ContainerName)
}

// podTunnelOp composes verb:tunnel over InvokeProvider with the {plugin_input:{method,config}}
// envelope verb:tunnel decodes (byte-compatible with the in-core invokeTunnel adapter, R3).
func podTunnelOp(ctx context.Context, exec *sdk.Executor, method string, cfg *spec.TunnelConfig) error {
	body, err := json.Marshal(map[string]any{"plugin_input": map[string]any{"method": method, "config": cfg}})
	if err != nil {
		return err
	}
	_, err = exec.InvokeProvider(ctx, "verb", "tunnel", sdk.OpRun, body, nil)
	return err
}

// shellJoin renders an argv into a single shell-safe command string (each token kit.ShellQuote'd)
// for the served host executor, which runs a script string rather than an argv (the direct-mode
// `podman run -d …` path — buildStartArgs produced the argv host-side).
func shellJoin(argv []string) string {
	quoted := make([]string, len(argv))
	for i, a := range argv {
		quoted[i] = kit.ShellQuote(a)
	}
	return strings.Join(quoted, " ")
}

// execErr runs a shell command over the served host executor, wrapping a failure with the op label
// and target name (mirroring the former StartCmd/StopCmd error text).
func execErr(exec *sdk.Executor, ctx context.Context, script, label, target string) error {
	if err := exec.VenueRunSilent(ctx, script); err != nil {
		return fmt.Errorf("plugin-deploy-pod %s (%s): %w", label, target, err)
	}
	return nil
}

// podPrepareVenue builds the overlay image via HostBuild("overlay") (the host prep+resolve,
// which returns the render envelope) + renders the overlay Containerfile IN THE CANDY (P11c —
// the pod-only render body dissolved out of core) + runs podman build + the deploy-name alias tag
// via the served host executor. Returns a host-local shell venue (the plugin walks nothing — pod
// bakes into the image). The no-overlay path (no add_candy plans) tags the base as the deploy-name
// alias so deployment-name-keyed commands resolve it when deploy-name != image-name.
func podPrepareVenue(ctx context.Context, exec *sdk.Executor, p lifecycleParams) (*pb.InvokeReply, error) {
	var opts spec.LifecycleOpts
	_ = json.Unmarshal(p.Opts, &opts)
	reqJSON, err := json.Marshal(spec.OverlayBuildRequest{
		Dir: p.Dir, DeployName: p.Name, Image: p.Image, Version: p.Version,
		DryRun: opts.DryRun, AssumeYes: opts.AssumeYes, AllowRepoChanges: opts.AllowRepoChanges,
		AllowRootTasks: opts.AllowRootTasks, WithServices: opts.WithServices,
	})
	if err != nil {
		return nil, err
	}
	resJSON, err := exec.HostBuild(ctx, "overlay", reqJSON)
	if err != nil {
		return nil, fmt.Errorf("plugin-deploy-pod prepare-venue: overlay build: %w", err)
	}
	var reply spec.OverlayBuildReply
	if err := json.Unmarshal(resJSON, &reply); err != nil {
		return nil, fmt.Errorf("plugin-deploy-pod prepare-venue: decode overlay reply: %w", err)
	}
	if reply.Error != "" {
		return nil, fmt.Errorf("plugin-deploy-pod prepare-venue: %s", reply.Error)
	}

	// The base box name — the key into reply.ResolvedProject.Boxes — is the SAME base the host prep
	// used (req.Image / req.DeployName).
	baseName := p.Image
	if baseName == "" {
		baseName = p.Name
	}

	// No overlay (no add_candy plans) → the base image is deploy-ready. Tag the deploy-name alias
	// so `charly config/start <deploy-name>` resolves the base image when deploy-name != image-name
	// (mirrors the former in-core pod-overlay Emit no-overlay branch). The host prep prepped the base
	// ref + metadata; the candy tags the alias via the served executor.
	resolvedImage := reply.BaseImage
	if len(reply.Plans) == 0 {
		if !opts.DryRun && reply.DeployName != "" && reply.BaseImage != "" {
			if err := tagDeployAlias(ctx, exec, reply, reply.BaseImage, opts); err != nil {
				return nil, err
			}
		}
	} else {
		// Overlay path: render the overlay Containerfile in the candy + podman build + tag.
		overlayRef, berr := buildOverlay(ctx, exec, reply, p.Dir, baseName, opts)
		if berr != nil {
			return nil, berr
		}
		resolvedImage = overlayRef
	}

	r := spec.PrepareVenueReply{Venue: spec.VenueDescriptor{Kind: "shell"}}
	if !opts.DryRun {
		r.Notes = []string{
			"Overlay image ready: " + resolvedImage,
			"To start the container, run: charly start " + reply.DeployName,
		}
		// Persist the concrete overlay ref ONLY when an overlay was actually built (add_candy
		// present, so resolvedImage differs from the base) — the host writes SaveDeployStateInput.
		if resolvedImage != "" && resolvedImage != reply.BaseImage {
			state, _ := json.Marshal(map[string]any{"ResolvedImage": resolvedImage})
			r.State = state
		}
	}
	return marshalReply(r)
}

// podStatus parses `charly status --json` for this deploy's row (the SAME best-effort scan the
// compiled-in pod lifecycle used).
func podStatus(ctx context.Context, exec *sdk.Executor, name string) (*pb.InvokeReply, error) {
	r, err := podCli(ctx, exec, true, true, "status", "--json")
	if err != nil {
		return marshalReply(map[string]any{"State": "unknown"})
	}
	state := "stopped"
	for _, line := range strings.Split(r.Stdout, "\n") {
		if !strings.Contains(line, name) {
			continue
		}
		switch {
		case strings.Contains(line, "running"):
			state = "running"
		case strings.Contains(line, "paused"):
			state = "paused"
		case strings.Contains(line, "crashed"):
			state = "crashed"
		}
		break
	}
	return marshalReply(map[string]any{"State": state, "Healthy": state == "running", "Details": map[string]string{"deploy": name}})
}

// podAttach runs the F12 interactive/live-stdio session (`charly shell` / `charly cmd`): the host
// resolved the venue command into p.Plan (#PodLiveStdioPlan); the plugin runs it over the served host
// executor via exec.RunInteractive, which inherits the operator's terminal (stdio never crosses the
// wire — only the script + the exit code). The exit round-trips as spec.PodExecReply.ExitCode so the
// host propagates it via *sdk.ExitCodeError. Distinct from podExec (OpShell, the #57 `charly service`
// capture leg). A spawn/signal failure (not a non-zero exit) is a real error.
func podAttach(ctx context.Context, exec *sdk.Executor, p lifecycleParams) (*pb.InvokeReply, error) {
	var opts spec.PodAttachOpts
	if err := json.Unmarshal(p.Plan, &opts); err != nil {
		return nil, fmt.Errorf("plugin-deploy-pod attach: decode opts: %w", err)
	}
	box, instance := deploykit.ParseDeployKey(p.Name)
	plan, err := resolvePodAttachPlan(ctx, exec, box, instance, opts)
	if err != nil {
		return nil, fmt.Errorf("plugin-deploy-pod attach: resolve plan: %w", err)
	}
	exit, err := exec.RunInteractive(ctx, plan.Script)
	if err != nil {
		return nil, fmt.Errorf("plugin-deploy-pod attach: %w", err)
	}
	return marshalReply(spec.PodExecReply{ExitCode: exit})
}

// podLogs streams/tails the container journal (F12): the host resolved the `<engine> logs`/`journalctl`
// stream command into p.Plan (#PodLiveStdioPlan); the plugin streams it LIVE to the operator via
// exec.RunStream (inherited stdout/stderr, host-held). This REPLACES the former podCli("logs") `charly
// logs` reentry — once `charly logs` routes through here (LifecycleTarget.Logs), that reentry would be
// an infinite loop.
func podLogs(ctx context.Context, exec *sdk.Executor, p lifecycleParams) (*pb.InvokeReply, error) {
	var opts spec.PodLogsOpts
	if err := json.Unmarshal(p.Opts, &opts); err != nil {
		return nil, fmt.Errorf("plugin-deploy-pod logs: decode opts: %w", err)
	}
	box, instance := deploykit.ParseDeployKey(p.Name)
	plan, err := resolvePodLogsPlan(ctx, exec, box, instance, opts)
	if err != nil {
		return nil, fmt.Errorf("plugin-deploy-pod logs: resolve plan: %w", err)
	}
	exit, err := exec.RunStream(ctx, plan.Script)
	if err != nil {
		return nil, fmt.Errorf("plugin-deploy-pod logs: %w", err)
	}
	return marshalReply(spec.PodExecReply{ExitCode: exit})
}

// podRebuild follows the pod rebuild sequence (image rebuild → check → deploy add → stop → config →
// start) — the path `charly update <pod-bed>` routes through (the disposable bed's fresh-rebuild R10
// gate). Each leg is a `charly` subcommand via the cli host-builder.
func podRebuild(ctx context.Context, exec *sdk.Executor, p lifecycleParams) (*pb.InvokeReply, error) {
	var ropts struct {
		DryRun       bool `json:"DryRun"`
		RebuildImage bool `json:"RebuildImage"`
	}
	_ = json.Unmarshal(p.Opts, &ropts)
	baseRef := p.Image
	if baseRef == "" {
		baseRef = p.Name
	}
	if ropts.DryRun {
		return marshalReply(struct{}{}) // dry-run: the host proxy prints nothing for Rebuild
	}
	if ropts.RebuildImage {
		if _, err := podCli(ctx, exec, false, false, "box", "build", baseRef); err != nil {
			return nil, err
		}
		if _, err := podCli(ctx, exec, false, false, "check", "box", baseRef); err != nil {
			return nil, err
		}
	}
	if _, err := podCli(ctx, exec, false, false, "bundle", "add", p.Name); err != nil {
		return nil, err
	}
	_, _ = podCli(ctx, exec, false, true, "stop", p.Name) // best-effort (preserve config)
	if _, err := podCli(ctx, exec, false, false, "config", p.Name); err != nil {
		return nil, err
	}
	if _, err := podCli(ctx, exec, false, false, "start", p.Name); err != nil {
		return nil, err
	}
	return marshalReply(struct{}{})
}

// podPostTeardown = `charly remove` (the container teardown, via the cli seam) + drop the synthesized
// <name>-overlay images itself via kit.RemoveImagesByReference (keep-image-gated; the host ships the
// resolved engine binary in EngineBin). pod ships no charly.yml RemoveEntries (host `charly remove`
// already cleaned the entry).
func podPostTeardown(ctx context.Context, exec *sdk.Executor, p lifecycleParams) (*pb.InvokeReply, error) {
	if _, err := podCli(ctx, exec, false, false, "remove", p.Name); err != nil {
		return nil, err
	}
	if !p.KeepImage {
		kit.RemoveImagesByReference(p.EngineBin, p.Name+"-overlay") // best-effort overlay-image drop
	}
	return marshalReply(spec.PostTeardownReply{})
}

// marshalReply marshals v into a *pb.InvokeReply.ResultJson.
func marshalReply(v any) (*pb.InvokeReply, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return &pb.InvokeReply{ResultJson: b}, nil
}
