package deploypod

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/opencharly/sdk"
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
}

// isLifecycleOp reports whether op is a substrate-lifecycle Op (vs. the OpExecute deploy walk).
func isLifecycleOp(op string) bool {
	switch op {
	case sdk.OpPrepareVenue, sdk.OpArtifactKey, sdk.OpPostApply, sdk.OpTeardownExecutor,
		sdk.OpPostTeardown, sdk.OpStart, sdk.OpStop, sdk.OpStatus, sdk.OpLogs, sdk.OpShell, sdk.OpRebuild:
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
		return cliOK(podCli(ctx, exec, false, false, "start", p.Name))
	case sdk.OpStop:
		return cliOK(podCli(ctx, exec, false, false, "stop", p.Name))
	case sdk.OpStatus:
		return podStatus(ctx, exec, p.Name)
	case sdk.OpLogs:
		return podLogs(ctx, exec, p)
	case sdk.OpShell:
		return cliOK(podCli(ctx, exec, false, false, append([]string{"shell", p.Name}, p.Cmd...)...))
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

// podPrepareVenue builds the overlay image HOST-SIDE via HostBuild("overlay") and returns a
// host-local shell venue (the plugin walks nothing — pod bakes into the image).
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
	var oreply spec.OverlayBuildReply
	if err := json.Unmarshal(resJSON, &oreply); err != nil {
		return nil, fmt.Errorf("plugin-deploy-pod prepare-venue: decode overlay reply: %w", err)
	}
	if oreply.Error != "" {
		return nil, fmt.Errorf("plugin-deploy-pod prepare-venue: %s", oreply.Error)
	}

	reply := spec.PrepareVenueReply{Venue: spec.VenueDescriptor{Kind: "shell"}}
	if !opts.DryRun {
		reply.Notes = []string{
			"Overlay image ready: " + oreply.OverlayRef,
			"To start the container, run: charly start " + oreply.DeployName,
		}
		// Persist the concrete overlay ref ONLY when an overlay was actually built (add_candy
		// present, so OverlayRef differs from the base) — the host writes SaveDeployStateInput.
		if oreply.OverlayRef != "" && oreply.OverlayRef != oreply.BaseImage {
			state, _ := json.Marshal(map[string]any{"ResolvedImage": oreply.OverlayRef})
			reply.State = state
		}
	}
	return marshalReply(reply)
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

// podLogs streams/tails the container journal via `charly logs`.
func podLogs(ctx context.Context, exec *sdk.Executor, p lifecycleParams) (*pb.InvokeReply, error) {
	var lopts struct {
		Follow bool `json:"Follow"`
		Tail   int  `json:"Tail"`
	}
	_ = json.Unmarshal(p.Opts, &lopts)
	argv := []string{"logs", p.Name}
	if lopts.Follow {
		argv = append(argv, "-f")
	}
	if lopts.Tail > 0 {
		argv = append(argv, "-n", fmt.Sprintf("%d", lopts.Tail))
	}
	return cliOK(podCli(ctx, exec, false, false, argv...))
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

// cliOK returns an empty-struct reply, propagating a cli error (Go spreads podCli's
// (CliReply, error) return straight into these params).
func cliOK(_ spec.CliReply, err error) (*pb.InvokeReply, error) {
	if err != nil {
		return nil, err
	}
	return marshalReply(struct{}{})
}

// marshalReply marshals v into a *pb.InvokeReply.ResultJson.
func marshalReply(v any) (*pb.InvokeReply, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return &pb.InvokeReply{ResultJson: b}, nil
}
