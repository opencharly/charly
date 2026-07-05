package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/spec"
)

// grpcSubstrateLifecycle implements the in-core substrateLifecycle interface by Invoking an
// OUT-OF-PROCESS substrate plugin's lifecycle Ops (F6) — the host→plugin counterpart of the
// plugin→host ExecutorService. It is the channel M4 externalizes the pod/vm lifecycles over.
//
// Every lifecycle Op is Invoked WITH the host's executor over the reverse channel
// (InvokeWithExecutor), so the plugin — which runs ON the host but out-of-process — can call back
// HostBuild("overlay"/"cli") + the reverse legs it needs (the compiled-in pod/vm lifecycles used
// the in-core runOverlayBuild + runCharlySubcommand directly; the externalized plugins reach the
// SAME engines through the reverse channel). Most Ops serve a host-local ShellExecutor; PostApply
// serves the LIVE venue executor (vm's nested pod-in-guest needs the guest). Every Op ships
// HostEnv{CharlyBin, Home} on op.Env (the plugin's own os.Executable() is the PLUGIN binary, so the
// host charly path must be handed to it). PrepareVenue re-materializes the returned VenueDescriptor
// into a real host-side DeployExecutor (the live executor never crosses the wire), persists the
// opaque State patch, and prints the Notes; PostTeardown removes the reply's charly.yml entry keys.
type grpcSubstrateLifecycle struct {
	prov *grpcProvider
}

// venueFromDescriptor re-materializes a VenueDescriptor into a real host-side DeployExecutor — the
// decouple point that lets a substrate lifecycle plugin run out-of-process: it returns a
// serializable venue description, the host owns the live executor.
func venueFromDescriptor(d spec.VenueDescriptor) (DeployExecutor, error) {
	switch d.Kind {
	case "":
		return nil, nil // no venue (e.g. TeardownExecutor declining → caller keeps its executor)
	case "shell":
		return ShellExecutor{}, nil
	case "ssh":
		return &SSHExecutor{User: d.User, Host: d.Host, Port: d.Port, Args: d.Args, ConnectTimeout: d.ConnectTimeout}, nil
	default:
		return nil, fmt.Errorf("substrate lifecycle: unknown venue descriptor kind %q", d.Kind)
	}
}

// hostEnvJSON returns the marshalled HostEnv for a lifecycle Op — the host charly binary path
// (os.Args[0], the CHARLY process's own path — NOT the plugin's) + the host home.
func hostEnvJSON() json.RawMessage {
	home, _ := os.UserHomeDir()
	env, _ := marshalJSON(spec.HostEnv{CharlyBin: os.Args[0], Home: home, Version: CharlyVersion()})
	return env
}

// marshalDeployOpParams marshals the common (name, dir, node, extra) args for a host→plugin deploy
// Op (the lifecycle Ops + the preresolver, R3). node is marshalled as the canonical BundleNode JSON
// so the plugin sees the SAME node the host decoded.
func marshalDeployOpParams(name, dir string, node *BundleNode, extra map[string]any) (json.RawMessage, error) {
	params := map[string]any{"name": name}
	if dir != "" {
		params["dir"] = dir
	}
	if node != nil {
		nj, err := marshalJSON(node)
		if err != nil {
			return nil, err
		}
		params["node"] = nj
	}
	for k, v := range extra {
		params[k] = v
	}
	return marshalJSON(params)
}

// lifecycleInvoke marshals the common args + HostEnv and Invokes the lifecycle op WITH the given
// executor over the reverse channel (so the plugin's HostBuild("overlay"/"cli") + reverse legs
// reach the host). The context may carry live overlay-build inputs (PrepareVenue), re-threaded by
// InvokeWithExecutor onto the reverse server.
func (l grpcSubstrateLifecycle) lifecycleInvoke(ctx context.Context, op, name, dir string, node *BundleNode, extra map[string]any, exec DeployExecutor) (*Result, error) {
	pj, err := marshalDeployOpParams(name, dir, node, extra)
	if err != nil {
		return nil, err
	}
	return l.prov.InvokeWithExecutor(ctx, &Operation{Reserved: l.prov.word, Op: op, Params: pj, Env: hostEnvJSON()}, exec, buildEngineContext{}, false, nil)
}

// lifecycleOptsFrom projects the host's EmitOpts onto the serializable spec.LifecycleOpts (the live
// ParentExec/ParentNode are re-attached host-side via the reverse channel, never serialized).
func lifecycleOptsFrom(opts EmitOpts) spec.LifecycleOpts {
	return spec.LifecycleOpts{
		DryRun:               opts.DryRun,
		AllowRepoChanges:     opts.AllowRepoChanges,
		AllowRootTasks:       opts.AllowRootTasks,
		WithServices:         opts.WithServices,
		AssumeYes:            opts.AssumeYes,
		Verify:               opts.Verify,
		Pull:                 opts.Pull,
		SkipIncompatible:     opts.SkipIncompatible,
		BuilderImageOverride: opts.BuilderImageOverride,
	}
}

func (l grpcSubstrateLifecycle) PrepareVenue(ctx context.Context, name, dir string, node *BundleNode, plans []*InstallPlan, opts EmitOpts) (DeployExecutor, error) {
	// Attach the LIVE overlay-build inputs to the ctx — InvokeWithExecutor re-threads them onto the
	// reverse server so a HostBuild("overlay") re-attaches the rich plans/parent-venue host-side.
	ctx = withOverlayBuildInputs(ctx, &overlayBuildInputs{plans: plans, parentExec: opts.ParentExec, parentNode: opts.ParentNode})
	// Pass the base image + version as explicit params (generic node fields) so the plugin builds
	// the OverlayBuildRequest without decoding the whole BundleNode.
	extra := map[string]any{"opts": lifecycleOptsFrom(opts)}
	if node != nil {
		extra["image"] = node.Image
		extra["version"] = node.Version
	}
	// Consult the substrate's host-side prepare hook GENERICALLY (never a "vm" branch): vm ships the
	// resolved spec.LifecyclePrepareInput (entity + ssh coords + prior state) under "prepare"; pod
	// registers no hook. The plugin does the actual venue lifecycle — this is only the DATA it needs.
	if hook, ok := lifecyclePrepareHookFor(l.prov.word); ok {
		prep, herr := hook(name, dir, node)
		if herr != nil {
			return nil, fmt.Errorf("substrate %q prepare-venue: resolve prepare data: %w", l.prov.word, herr)
		}
		extra["prepare"] = prep
	}
	res, err := l.lifecycleInvoke(ctx, sdk.OpPrepareVenue, name, dir, node, extra, ShellExecutor{})
	if err != nil {
		return nil, err
	}
	var reply spec.PrepareVenueReply
	if err := json.Unmarshal(res.JSON, &reply); err != nil {
		return nil, fmt.Errorf("substrate %q prepare-venue: decode reply: %w", l.prov.word, err)
	}
	for _, note := range reply.Notes {
		fmt.Println(note)
	}
	// Persist the opaque deploy-entry State patch host-side (the plugin cannot touch charly.yml):
	// pod ships {ResolvedImage}; vm ships {vm_state}. saveDeployState is the generic writer.
	if len(reply.State) > 0 && !opts.DryRun {
		var in SaveDeployStateInput
		if err := json.Unmarshal(reply.State, &in); err != nil {
			return nil, fmt.Errorf("substrate %q prepare-venue: decode state: %w", l.prov.word, err)
		}
		boxKey, instKey := parseDeployKey(name)
		saveDeployState(boxKey, instKey, in)
	}
	return venueFromDescriptor(reply.Venue)
}

func (l grpcSubstrateLifecycle) ArtifactKey(name string, node *BundleNode) string {
	res, err := l.lifecycleInvoke(context.Background(), sdk.OpArtifactKey, name, "", node, nil, ShellExecutor{})
	if err != nil || len(res.JSON) == 0 {
		return "" // best-effort: caller keys by the deploy name on empty
	}
	var out struct {
		Key string `json:"key"`
	}
	if json.Unmarshal(res.JSON, &out) != nil {
		return ""
	}
	return out.Key
}

func (l grpcSubstrateLifecycle) PostApply(ctx context.Context, name, dir string, node *BundleNode, exec DeployExecutor, opts EmitOpts) error {
	// PostApply serves the LIVE venue executor (vm's nested pod-in-guest walks the guest); a host
	// ShellExecutor when the caller has no live venue (pod, whose PostApply is a no-op).
	if exec == nil {
		exec = ShellExecutor{}
	}
	_, err := l.lifecycleInvoke(ctx, sdk.OpPostApply, name, dir, node, map[string]any{"opts": lifecycleOptsFrom(opts)}, exec)
	return err
}

func (l grpcSubstrateLifecycle) TeardownExecutor(name string, node *BundleNode) (DeployExecutor, error) {
	res, err := l.lifecycleInvoke(context.Background(), sdk.OpTeardownExecutor, name, "", node, nil, ShellExecutor{})
	if err != nil {
		return nil, err
	}
	if len(res.JSON) == 0 {
		return nil, nil // caller keeps its ResolveTarget-selected executor
	}
	var d spec.VenueDescriptor
	if err := json.Unmarshal(res.JSON, &d); err != nil {
		return nil, fmt.Errorf("substrate %q teardown-executor: decode venue descriptor: %w", l.prov.word, err)
	}
	return venueFromDescriptor(d)
}

func (l grpcSubstrateLifecycle) PostTeardown(name string, node *BundleNode, keepImage bool) error {
	// Host-side substrate cleanup the plugin cannot do (vm: ephemeral-lifecycle teardown — systemd
	// timers + libvirt snapshot refcounts). Consulted GENERICALLY by word (pod registers none).
	if hook, ok := lifecyclePostTeardownHookFor(l.prov.word); ok {
		if herr := hook(name, node); herr != nil {
			fmt.Fprintf(os.Stderr, "warning: substrate %q post-teardown host hook: %v\n", l.prov.word, herr)
		}
	}
	res, err := l.lifecycleInvoke(context.Background(), sdk.OpPostTeardown, name, "", node, map[string]any{"keep_image": keepImage}, ShellExecutor{})
	if err != nil {
		return err
	}
	// The plugin cannot touch charly.yml — the host removes the reply's deploy-entry keys.
	if len(res.JSON) > 0 {
		var reply spec.PostTeardownReply
		if err := json.Unmarshal(res.JSON, &reply); err != nil {
			return fmt.Errorf("substrate %q post-teardown: decode reply: %w", l.prov.word, err)
		}
		for _, key := range reply.RemoveEntries {
			_ = removeVmDeployEntry(key)
		}
	}
	return nil
}

func (l grpcSubstrateLifecycle) Start(ctx context.Context, name string, node *BundleNode) error {
	_, err := l.lifecycleInvoke(ctx, sdk.OpStart, name, "", node, nil, ShellExecutor{})
	return err
}

func (l grpcSubstrateLifecycle) Stop(ctx context.Context, name string, node *BundleNode) error {
	_, err := l.lifecycleInvoke(ctx, sdk.OpStop, name, "", node, nil, ShellExecutor{})
	return err
}

func (l grpcSubstrateLifecycle) Status(ctx context.Context, name string, node *BundleNode) (StatusInfo, error) {
	res, err := l.lifecycleInvoke(ctx, sdk.OpStatus, name, "", node, nil, ShellExecutor{})
	if err != nil {
		return StatusInfo{}, err
	}
	var si StatusInfo
	if len(res.JSON) > 0 {
		if err := json.Unmarshal(res.JSON, &si); err != nil {
			return StatusInfo{}, fmt.Errorf("substrate %q status: decode: %w", l.prov.word, err)
		}
	}
	return si, nil
}

func (l grpcSubstrateLifecycle) Logs(ctx context.Context, name string, node *BundleNode, opts LogsOpts) error {
	_, err := l.lifecycleInvoke(ctx, sdk.OpLogs, name, "", node, map[string]any{"opts": opts}, ShellExecutor{})
	return err
}

func (l grpcSubstrateLifecycle) Shell(ctx context.Context, name string, node *BundleNode, cmd []string) error {
	_, err := l.lifecycleInvoke(ctx, sdk.OpShell, name, "", node, map[string]any{"cmd": cmd}, ShellExecutor{})
	return err
}

func (l grpcSubstrateLifecycle) Rebuild(ctx context.Context, name string, node *BundleNode, opts RebuildOpts) error {
	_, err := l.lifecycleInvoke(ctx, sdk.OpRebuild, name, "", node, map[string]any{"opts": opts}, ShellExecutor{})
	return err
}
