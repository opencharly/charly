package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// grpcSubstrateLifecycle implements the in-core substrateLifecycle interface by Invoking an
// OUT-OF-PROCESS substrate plugin's lifecycle Ops (F6) — the host→plugin counterpart of the
// plugin→host ExecutorService. It is the channel M4 externalizes the pod/vm lifecycles over.
//
// Every lifecycle Op is Invoked WITH the host's executor over the reverse channel
// (InvokeWithExecutor), so the plugin — which runs ON the host but out-of-process — can call back
// HostBuild("overlay"/"cli") + the reverse legs it needs (the compiled-in pod/vm lifecycles used
// the in-core overlay build + runCharlySubcommand directly; the externalized plugins reach the
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
func venueFromDescriptor(d spec.VenueDescriptor) (deploykit.DeployExecutor, error) {
	switch d.Kind {
	case "":
		return nil, nil // no venue (e.g. VenueExecutor declining → caller keeps its executor)
	case "shell":
		return kit.ShellExecutor{}, nil
	case "ssh":
		return &kit.SSHExecutor{User: d.User, Host: d.Host, Port: d.Port, Args: d.Args, ConnectTimeout: d.ConnectTimeout}, nil
	default:
		return nil, fmt.Errorf("substrate lifecycle: unknown venue descriptor kind %q", d.Kind)
	}
}

// hostEnvJSON returns the marshalled HostEnv for a lifecycle Op — the host charly binary
// path (the CHARLY process's own RESOLVED path — NOT the plugin's) + the host home. Uses
// os.Executable() (the absolute binary path), NOT os.Args[0]: argv[0] is the invocation
// name (a bare "charly" when run as `charly …`), which the vm plugin's scp-into-guest
// (EnsureCharlyInGuest) rejects as "not a regular file".
func hostEnvJSON() json.RawMessage {
	home, _ := os.UserHomeDir()
	charlyBin, err := os.Executable()
	if err != nil || charlyBin == "" {
		charlyBin = os.Args[0] // last-resort fallback
	}
	env, _ := marshalJSON(spec.HostEnv{CharlyBin: charlyBin, Home: home, Version: CharlyVersion()})
	return env
}

// marshalDeployOpParams marshals the common (name, dir, node, extra) args for a host→plugin deploy
// Op (the lifecycle Ops + the preresolver, R3). node is marshalled as the canonical BundleNode JSON
// so the plugin sees the SAME node the host decoded.
func marshalDeployOpParams(name, dir string, node *spec.BundleNode, extra map[string]any) (json.RawMessage, error) {
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
func (l grpcSubstrateLifecycle) lifecycleInvoke(ctx context.Context, op, name, dir string, node *spec.BundleNode, extra map[string]any, exec deploykit.DeployExecutor) (*Result, error) {
	pj, err := marshalDeployOpParams(name, dir, node, extra)
	if err != nil {
		return nil, err
	}
	return l.prov.InvokeWithExecutor(ctx, &Operation{Reserved: l.prov.word, Op: op, Params: pj, Env: hostEnvJSON()}, exec, buildEngineContext{}, false, nil)
}

// lifecycleOptsFrom projects the host's EmitOpts onto the serializable spec.LifecycleOpts (the live
// ParentExec/ParentNode are re-attached host-side via the reverse channel, never serialized).
func lifecycleOptsFrom(opts deploykit.EmitOpts) spec.LifecycleOpts {
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

func (l grpcSubstrateLifecycle) PrepareVenue(ctx context.Context, name, dir string, node *spec.BundleNode, plans []*deploykit.InstallPlan, opts deploykit.EmitOpts) (deploykit.DeployExecutor, error) {
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
	// No host-side "prepare" data injection (FINAL/K5 unit 6a, M4b — hard cutover): the deleted
	// lifecyclePrepareHook indirection is gone. Every Lifecycle:true substrate ALREADY owns
	// OpPrepareVenue in its own plugin, so it self-serves any LoadUnified-coupled data it needs via
	// the generic "deploy-entity-resolve" HostBuild seam (candy/plugin-deploy-vm's vmPrepareVenue is
	// the reference) instead of receiving it host-precomputed here.
	res, err := l.lifecycleInvoke(ctx, sdk.OpPrepareVenue, name, dir, node, extra, kit.ShellExecutor{})
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
	// pod ships {ResolvedImage}. saveDeployState is the generic writer, keyed by the RAW
	// (unsanitized) deploy name — correct for pod's own plain top-level naming today. vm does
	// NOT ship a State patch here (RCA #6, FINAL/K5 unit 6a, hard cutover): its PrepareVenue used
	// to also ship {vm_state}, persisted through THIS generic path under the wrong (unsanitized)
	// key — a second, independent writer racing candy/plugin-vm's own canonical
	// "vm:"+VmDomainIdentity(name)-keyed persist (vm_create_orchestrate.go's hostConfigPersist),
	// and for a NESTED (dotted) deploy name, poisoning the overlay on every subsequent load
	// (charly/unified.go's validateDeploymentName dot-rejection). The vm substrate now owns its
	// own persistence end to end — see candy/plugin-deploy-vm/lifecycle.go's PrepareVenue.
	if len(reply.State) > 0 && !opts.DryRun {
		var in deploykit.SaveDeployStateInput
		if err := json.Unmarshal(reply.State, &in); err != nil {
			return nil, fmt.Errorf("substrate %q prepare-venue: decode state: %w", l.prov.word, err)
		}
		boxKey, instKey := deploykit.ParseDeployKey(name)
		deploykit.SaveDeployState(boxKey, instKey, in, marshalDeployNode)
	}
	return venueFromDescriptor(reply.Venue)
}

func (l grpcSubstrateLifecycle) ArtifactKey(name string, node *spec.BundleNode) string {
	res, err := l.lifecycleInvoke(context.Background(), sdk.OpArtifactKey, name, "", node, nil, kit.ShellExecutor{})
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

func (l grpcSubstrateLifecycle) PostApply(ctx context.Context, name, dir string, node *spec.BundleNode, exec deploykit.DeployExecutor, opts deploykit.EmitOpts) error {
	// PostApply serves the LIVE venue executor (vm's nested pod-in-guest walks the guest); a host
	// ShellExecutor when the caller has no live venue (pod, whose PostApply is a no-op).
	if exec == nil {
		exec = kit.ShellExecutor{}
	}
	_, err := l.lifecycleInvoke(ctx, sdk.OpPostApply, name, dir, node, map[string]any{"opts": lifecycleOptsFrom(opts)}, exec)
	return err
}

func (l grpcSubstrateLifecycle) VenueExecutor(name string, node *spec.BundleNode) (deploykit.DeployExecutor, error) {
	res, err := l.lifecycleInvoke(context.Background(), sdk.OpTeardownExecutor, name, "", node, nil, kit.ShellExecutor{})
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

func (l grpcSubstrateLifecycle) PostTeardown(name string, node *spec.BundleNode, keepImage bool) error {
	// Host-side substrate cleanup the plugin cannot do (vm: ephemeral-lifecycle teardown — systemd
	// timers + libvirt snapshot refcounts). Consulted GENERICALLY by word (pod registers none).
	if hook, ok := lifecyclePostTeardownHookFor(l.prov.word); ok {
		if herr := hook(name, node); herr != nil {
			fmt.Fprintf(os.Stderr, "warning: substrate %q post-teardown host hook: %v\n", l.prov.word, herr)
		}
	}
	// Ship the resolved engine binary (host resolves podman/docker/auto) so a plugin dropping its
	// deploy's images (pod: the <name>-overlay drop) needs no in-plugin engine detection. Generic —
	// vm ignores it.
	res, err := l.lifecycleInvoke(context.Background(), sdk.OpPostTeardown, name, "", node,
		map[string]any{"keep_image": keepImage, "engine_bin": kit.EngineBinary(podDeployEngine(node))}, kit.ShellExecutor{})
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

func (l grpcSubstrateLifecycle) Start(ctx context.Context, name string, node *spec.BundleNode) error {
	// A substrate with a start-plan hook (pod, the K4 deep-body move) resolves the
	// PodLifecyclePlan host-side, threads it to OpStart, and BRACKETS the shared arbiter claim
	// (acquire before, release on the failure path — pod-scoped by the hook, so a vm that shells
	// its own `charly vm start` never double-claims). A substrate with no hook (vm) plain-invokes.
	planHook, hasPlan := lifecycleStartPlanHooks[l.prov.word]
	if !hasPlan {
		_, err := l.lifecycleInvoke(ctx, sdk.OpStart, name, "", node, nil, kit.ShellExecutor{})
		return err
	}
	box, instance := deploykit.ParseDeployKey(name)
	if node != nil {
		if _, err := acquireResourceForClaimant(name, *node, false); err != nil {
			return err
		}
	}
	planJSON, err := planHook(ctx, box, instance)
	if err != nil {
		releaseResourceClaim(name) // release-on-failure: a plan-resolve error must not leak the claim
		return err
	}
	if _, err = l.lifecycleInvoke(ctx, sdk.OpStart, name, "", node, map[string]any{"plan": planJSON}, kit.ShellExecutor{}); err != nil {
		releaseResourceClaim(name) // release-on-failure: a failed start must not leak the claim
	}
	return err
}

func (l grpcSubstrateLifecycle) Stop(ctx context.Context, name string, node *spec.BundleNode) error {
	planHook, hasPlan := lifecycleStopPlanHooks[l.prov.word]
	if !hasPlan {
		_, err := l.lifecycleInvoke(ctx, sdk.OpStop, name, "", node, nil, kit.ShellExecutor{})
		return err
	}
	box, instance := deploykit.ParseDeployKey(name)
	planJSON, err := planHook(ctx, box, instance)
	if err != nil {
		return err
	}
	_, err = l.lifecycleInvoke(ctx, sdk.OpStop, name, "", node, map[string]any{"plan": planJSON}, kit.ShellExecutor{})
	releaseResourceClaim(name) // release the persistent claim after stop (the F6 bracket this file's header describes)
	return err
}

func (l grpcSubstrateLifecycle) Status(ctx context.Context, name string, node *spec.BundleNode) (StatusInfo, error) {
	res, err := l.lifecycleInvoke(ctx, sdk.OpStatus, name, "", node, nil, kit.ShellExecutor{})
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

func (l grpcSubstrateLifecycle) Logs(ctx context.Context, name string, node *spec.BundleNode, opts LogsOpts) error {
	// A substrate with a logs plan resolver (pod, F12) resolves the #PodLiveStdioPlan host-side (the
	// `<engine> logs`/`journalctl` stream command) and threads it so the plugin streams it via
	// exec.RunStream — killing the former podCli("logs") `charly logs` reentry (an infinite loop once
	// `charly logs` routes through here). A substrate without one (vm) keeps the plain opts-threaded
	// OpLogs path (its `charly vm console` cli reentry). Logs runs on the host ShellExecutor.
	extra := map[string]any{"opts": opts}
	if planHook, ok := lifecycleLogsPlanHooks[l.prov.word]; ok {
		box, instance := deploykit.ParseDeployKey(name)
		planJSON, err := planHook(ctx, box, instance, opts)
		if err != nil {
			return err
		}
		extra["plan"] = planJSON
	}
	_, err := l.lifecycleInvoke(ctx, sdk.OpLogs, name, "", node, extra, kit.ShellExecutor{})
	return err
}

// Attach drives the F12 interactive/live-stdio session (`charly shell` / `charly cmd`) over the
// substrate's live VENUE executor — pod → host ShellExecutor (the resolver's full `podman exec/run
// -it` argv runs on the host), vm → the guest *SSHExecutor (RunInteractive wraps the resolver's
// in-guest command in `ssh -t <alias>`). The venue executor is the SAME one a running deploy's
// teardown replays over (VenueExecutor: vm → guest SSH, pod → nil → host ShellExecutor), resolved
// side-effect-free. The host RESOLVES the #PodLiveStdioPlan (#59 inventory stays core) and threads it
// to OpAttach; the plugin decodes it and calls exec.RunInteractive (stdio host-held, never crosses the
// wire). NO arbiter bracket — an interactive session claims no exclusive resource. A non-zero exit
// round-trips as spec.PodExecReply.ExitCode → *sdk.ExitCodeError (main.go maps it to the process exit).
func (l grpcSubstrateLifecycle) Attach(ctx context.Context, name string, node *spec.BundleNode, cmd []string, tty bool) error {
	planHook, ok := lifecycleAttachPlanHooks[l.prov.word]
	if !ok {
		return fmt.Errorf("substrate %q: interactive attach not supported", l.prov.word)
	}
	venue := deploykit.DeployExecutor(kit.ShellExecutor{})
	if ve, verr := l.VenueExecutor(name, node); verr == nil && ve != nil {
		venue = ve // vm → guest SSHExecutor; pod → nil → host ShellExecutor
	}
	box, instance := deploykit.ParseDeployKey(name)
	planJSON, err := planHook(ctx, box, instance, cmd, tty)
	if err != nil {
		return err
	}
	res, err := l.lifecycleInvoke(ctx, sdk.OpAttach, name, "", node, map[string]any{"plan": planJSON}, venue)
	if err != nil {
		return err
	}
	var reply spec.PodExecReply
	if res != nil && len(res.JSON) > 0 {
		if err := json.Unmarshal(res.JSON, &reply); err != nil {
			return fmt.Errorf("substrate %q attach: decode reply: %w", l.prov.word, err)
		}
	}
	if reply.ExitCode != 0 {
		return &sdk.ExitCodeError{Code: reply.ExitCode}
	}
	return nil
}

// Shell drives a NON-interactive in-container exec (the K4 `charly service` move — the host
// resolved the full `<engine> exec <ctr> <tool> <op> <svc>` argv, cmd). The plugin RunCaptures it
// over the served executor and returns a spec.PodExecReply {Output, ExitCode}; the host REPRINTS the
// output (placement-agnostic — an out-of-process plugin's stdout is not charly's) and PROPAGATES a
// non-zero ExitCode exactly via *sdk.ExitCodeError (main.go maps it to the process exit), preserving
// the container command's exit code through the passthrough→capture semantics change. Interactive
// `charly shell` is a CORE command (host-process TTY, F12/#62) and never reaches here.
func (l grpcSubstrateLifecycle) Shell(ctx context.Context, name string, node *spec.BundleNode, cmd []string) error {
	res, err := l.lifecycleInvoke(ctx, sdk.OpShell, name, "", node, map[string]any{"cmd": cmd}, kit.ShellExecutor{})
	if err != nil {
		return err
	}
	var reply spec.PodExecReply
	if res != nil && len(res.JSON) > 0 {
		if err := json.Unmarshal(res.JSON, &reply); err != nil {
			return fmt.Errorf("substrate %q shell: decode exec reply: %w", l.prov.word, err)
		}
	}
	if reply.Output != "" {
		fmt.Print(reply.Output)
	}
	if reply.ExitCode != 0 {
		return &sdk.ExitCodeError{Code: reply.ExitCode}
	}
	return nil
}

func (l grpcSubstrateLifecycle) Rebuild(ctx context.Context, name string, node *spec.BundleNode, opts RebuildOpts) error {
	_, err := l.lifecycleInvoke(ctx, sdk.OpRebuild, name, "", node, map[string]any{"opts": opts}, kit.ShellExecutor{})
	return err
}
