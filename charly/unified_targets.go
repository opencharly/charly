package main

// unified_targets.go — The unified deploy-target abstraction.
//
// UnifiedDeployTarget/LifecycleTarget via pluginDeployTarget (S3b — the thin, DATA-ONLY proxy left
// behind once the deploy dispatch orchestration moved to candy/plugin-bundle over the ONE generic
// sdk.OpDeployDispatch envelope, see candy/plugin-bundle/deploy_target.go and
// CHANGELOG/2026.203.0212.md for the full migration narrative), and the ResolveTarget dispatcher.
// ALL FIVE substrates
// (local/vm/pod/k8s/android) are EXTERNAL — each resolves to pluginDeployTarget, which holds ONLY
// plain data (name/word/hasLifecycle/hasPreresolve/node) and a live venue executor, never a
// core-private *grpcProvider (that type is constructed at plugin-CONNECT time — clause-M, cannot
// move — so nothing holding one can live in a plugin). Every method dispatches to
// candy/plugin-bundle's Invoke(OpDeployDispatch), discriminated by an `op` field, which in turn
// reaches the ACTUAL substrate provider via its own sdk.Executor.InvokeProvider (S1).
//
// Two things stay core-resident by design, wrapping the dispatch rather than living inside it:
//   - The arbiter acquire/release BRACKET (Start/Stop only) — CHARLY_PREEMPT_LEASE is a
//     process-env mutex that only behaves correctly in the SAME OS process as the host
//     (arbiter_bracket.go).
//   - The pod Start/Stop/Attach/Logs plan-hook read (pod_lifecycle_dispatch.go) — pure ctx-opts
//     marshals with zero core-only dependency of their own; reused unchanged, just called from
//     here instead of from the former core-resident substrate lifecycle proxy (deleted, S3b —
//     see CHANGELOG/2026.203.0212.md).
//
// There is no per-kind dispatch switch in the cmd files — the kind lives behind the adapter
// method.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"

	"github.com/opencharly/sdk/kit"
)

// runUnifiedTargetChecks runs a deploy-scope check list via a live-mode Runner
// over exec, filtering to opts.OnlyIDs when set and reporting per-check failures
// to stderr. kind ("pod"/"vm"/"host", from the adapter's Kind()) labels both the
// no-executor and the summary errors; nodeName is the deploy identifier. Shared
// by Pod/Vm/the local deploy target.Test — the three were byte-identical bar the
// kind/name labels (R3).
func runUnifiedTargetChecks(ctx context.Context, exec deploykit.DeployExecutor, kind, nodeName string, checks []spec.Op, opts TestOpts) error {
	onlyIDs := make(map[string]bool, len(opts.OnlyIDs))
	for _, id := range opts.OnlyIDs {
		onlyIDs[id] = true
	}
	filtered := checks
	if len(onlyIDs) > 0 {
		filtered = filtered[:0]
		for _, c := range checks {
			if onlyIDs[c.ID] {
				filtered = append(filtered, c)
			}
		}
	}
	if exec == nil {
		return fmt.Errorf("%s %q: no executor configured", kind, nodeName)
	}
	runner := newCheckRunner(kit.RunnerConfig{Exec: exec, Mode: RunModeLive})
	results := runner.Run(ctx, filtered)
	failed := 0
	for _, r := range results {
		if r.Status == spec.StatusFail {
			failed++
			id := ""
			if r.Op != nil {
				id = r.Op.ID
			}
			fmt.Fprintf(os.Stderr, "FAIL %s: %s\n", id, r.Message)
			if opts.StopOnFail {
				return fmt.Errorf("test stopped at first failure: %s", id)
			}
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d %s check(s) failed", failed, kind)
	}
	return nil
}

// ---------------------------------------------------------------------------
// pluginDeployTarget — the thin, data-only UnifiedDeployTarget/LifecycleTarget proxy (S3b).
// ---------------------------------------------------------------------------

// pluginDeployTarget implements UnifiedDeployTarget/LifecycleTarget by dispatching every method to
// candy/plugin-bundle's Invoke(OpDeployDispatch) — it holds ONLY plain data (constructible from
// data alone, per call, never a stored core-private registry object) plus the live executor
// dispatchDeployTarget threads onto the reverse server for the plugin's OWN substrate dispatch.
type pluginDeployTarget struct {
	name          string
	word          string
	hasLifecycle  bool
	hasPreresolve bool

	// node is the dispatch-merged BundleNode (set by ResolveTarget). nil for a ref-based deploy
	// with no charly.yml entry.
	node *spec.BundleNode

	// exec is the INITIAL executor ResolveTarget computed from the node's host: field
	// (deploykit.RootExecutorForDeployNode) — the plugin may override it internally for a
	// lifecycle substrate (PrepareVenue) and reports the FINAL one back via venueJSON, which
	// subsequent calls on this SAME target reuse (see the venue field below).
	exec deploykit.DeployExecutor

	// venueJSON is the marshalled spec.VenueDescriptor for the CURRENT venue — nil until the
	// first "add" dispatch reports one back. Threaded on every subsequent dispatch call
	// (update/del/start/stop/status/logs/shell/attach/rebuild) so the plugin reuses the SAME
	// venue instead of re-running PrepareVenue.
	venueJSON json.RawMessage

	// nodeOnly mirrors `charly bundle add --node-only` (set by the dispatcher from
	// deployAddCmd.NodeOnly, matching the pre-S3b type-assertion pattern — see
	// bundle_add_cmd.go).
	nodeOnly bool

	// KeepRepoChanges / KeepServices / KeepImage are the `charly bundle del --keep-…` teardown
	// gates, set by the del-command dispatcher (host_build_deploy_node_del_dispatch.go) via the
	// SAME type-assertion pattern the pre-S3b former core-resident deploy target used (Del's
	// signature is the fixed UnifiedDeployTarget interface contract, with no room for extra params).
	KeepRepoChanges bool
	KeepServices    bool
	KeepImage       bool

	// build is the host-ENGINE context (project Config + dir + DistroCfg) the RunHostStep
	// reverse leg needs when the plugin walks a plan carrying a host-engine step kind. Populated
	// by Add from the DeployContext.
	build buildEngineContext

	// paths OPTIONALLY overrides the ledger root — a TEST redirecting to a temp dir instead of the
	// operator's real ~/.config/opencharly/installed/, mirroring the pre-S3b former core-resident
	// deploy target's settable `paths *kit.LedgerPaths` field exactly (same injection pattern, threaded to the
	// plugin as req.LedgerRoot since a live *kit.LedgerPaths cannot cross the wire). nil (the
	// default) — the plugin uses kit.DefaultLedgerPaths().
	paths *kit.LedgerPaths
}

func (t *pluginDeployTarget) Name() string                       { return t.name }
func (t *pluginDeployTarget) Kind() string                       { return "host" } // ops run on the host venue via the reverse channel
func (t *pluginDeployTarget) Executor() deploykit.DeployExecutor { return t.exec }

// distroCfgJSON marshals t.build.DistroCfg (a plain sdk/buildkit type, no core-only coupling) for
// the wire — recordDeploy's ReverseOpPackageRemove uninstall-cmd render needs it, now plugin-side.
func (t *pluginDeployTarget) distroCfgJSON() json.RawMessage {
	if t.build.DistroCfg == nil {
		return nil
	}
	b, err := json.Marshal(t.build.DistroCfg)
	if err != nil {
		return nil
	}
	return b
}

// hostEnvJSON returns the marshalled spec.HostEnv (CharlyBin/Home/Version) for the dispatch
// request — R10 bed-found bug #5 (S3b): the deleted substrate_lifecycle_grpc.go computed this
// HOST-side via os.Executable(), which resolves correctly to the charly binary only when called
// IN CORE — a plugin's os.Executable() would resolve to the PLUGIN binary for an out-of-process
// placement (candy/plugin-bundle's own port of this helper marshalled a bare zero-value
// spec.HostEnv{} instead, never actually calling it, which is why EnsureCharlyInGuest read an
// EMPTY CharlyBin path: "open : no such file or directory"). Core is the one place that reliably
// knows its OWN binary regardless of ANY plugin's placement, so it computes this ONCE per
// dispatch call and threads it on the wire; candy/plugin-bundle forwards it verbatim to every
// lifecycle Op instead of computing its own (mirrors the pre-move helper's exact fallback chain).
func hostEnvJSON() json.RawMessage {
	home, _ := os.UserHomeDir()
	charlyBin, err := os.Executable()
	if err != nil || charlyBin == "" {
		charlyBin = os.Args[0] // last-resort fallback
	}
	env, _ := marshalJSON(spec.HostEnv{CharlyBin: charlyBin, Home: home, Version: CharlyVersion()})
	return env
}

// dispatch is the shared per-method wire call: build the request's common fields, thread the
// live executor + build context + rebootable, Invoke, and — for a reply carrying a NEW venue —
// update t.venueJSON so the NEXT call on this same target reuses it.
func (t *pluginDeployTarget) dispatch(ctx context.Context, req spec.DeployTargetDispatchRequest) (spec.DeployTargetDispatchReply, error) {
	req.Name = t.name
	req.Word = t.word
	req.HasLifecycle = t.hasLifecycle
	req.HasPreresolve = t.hasPreresolve
	req.Node = t.node
	req.NodeOnly = t.nodeOnly
	req.HostEnvJSON = hostEnvJSON()
	if len(t.venueJSON) > 0 && len(req.VenueJSON) == 0 {
		req.VenueJSON = t.venueJSON
	}
	if t.paths != nil && req.LedgerRoot == "" {
		req.LedgerRoot = t.paths.Root
	}
	reply, err := dispatchDeployTarget(ctx, req, t.exec, t.build, t.hasLifecycle)
	if err != nil {
		return reply, err
	}
	if len(reply.VenueJSON) > 0 {
		t.venueJSON = reply.VenueJSON
	}
	return reply, nil
}

// applyParentExecOverride implements the FIX ROUND regression fix (R10 bed-found, S3b
// follow-up): a NESTED external deploy with no lifecycle hook of its own (a
// `local:`/`android:`/`k8s:` child under a vm/pod, tree position) must apply INSIDE the parent's
// venue, never the operator host — mirrors the pre-move former core-resident deploy target's
// apply's `else if opts.ParentExec != nil { t.exec = opts.ParentExec }` swap exactly (see
// CHANGELOG/2026.203.0212.md for the file this moved from; a lifecycle substrate, vm/pod,
// composes its OWN nested venue INSIDE PrepareVenue instead, so this override is the non-lifecycle
// branch only — a no-op when t.hasLifecycle or opts.ParentExec is nil).
//
// t.exec is mutated to the live parent executor: it becomes the executor threaded onto
// candy/plugin-bundle's in-proc reverse channel (dispatchDeployTarget's `exec` param) for EVERY
// reverse leg this dispatch call drives (RunSystem/RunUser/RunHostStep/…). Because a live Go
// interface value cannot itself cross the []byte wire to the plugin's decoded
// spec.DeployTargetDispatchRequest, the SAME executor is ALSO flattened into the returned
// venue_json (kit.DescriptorFromExecutor) — deriveChildExecutorForPath's "ssh" transport hop
// (bundle_add_cmd.go) is always a plain *kit.SSHExecutor for a single vm-guest hop, never a
// composed *kit.NestedExecutor, so it round-trips faithfully. The caller threads the result as
// the dispatch request's VenueJSON, so resolveRootExecutor (candy/plugin-bundle/deploy_target.go)
// re-materializes the IDENTICAL guest venue instead of falling back to
// deploykit.RootExecutorForDeployNode(req.Node), which — for a nested child carrying no `host:`
// field of its own — silently defaults to the operator's host ShellExecutor (the bug: every
// plain-vm nested child's plan/step walk ran on the OPERATOR'S HOST instead of the guest venue).
//
// Extracted as its own method (rather than inlined at the one call site) so the ordering
// invariant — t.exec is ALWAYS mutated together with the returned venue_json, never one without
// the other — has a single, directly unit-tested unit (unified_targets_test.go).
func (t *pluginDeployTarget) applyParentExecOverride(opts deploykit.EmitOpts) json.RawMessage {
	if t.hasLifecycle || opts.ParentExec == nil {
		return nil
	}
	t.exec = opts.ParentExec
	d := kit.DescriptorFromExecutor(opts.ParentExec)
	if d.Kind == "" {
		return nil
	}
	pj, err := json.Marshal(d)
	if err != nil {
		return nil
	}
	return pj
}

func (t *pluginDeployTarget) Add(ctx context.Context, dctx *DeployContext, plans []*deploykit.InstallPlan, opts deploykit.EmitOpts) error {
	if dctx != nil {
		t.node = dctx.Node
		t.build = buildEngineContext{Cfg: dctx.Cfg, ProjectDir: dctx.Dir, DistroCfg: dctx.DistroCfg}
	}
	var candyList []spec.CandyReader
	var secretEnv map[string]string
	dir := ""
	if dctx != nil {
		dir = dctx.Dir
	}
	if dir != "" {
		var serr error
		candyList, secretEnv, serr = prepareCandySecrets(plans, dir)
		if serr != nil {
			return fmt.Errorf("external deploy %q: loading candies for secret resolution: %w", t.name, serr)
		}
	}

	views := make([]spec.InstallPlanView, 0, len(plans))
	for _, p := range plans {
		if p != nil {
			views = append(views, deploykit.WireView(p))
		}
	}
	plansJSON, err := json.Marshal(views)
	if err != nil {
		return fmt.Errorf("external deploy %q: marshal plans: %w", t.name, err)
	}
	// Project opts onto the wire-safe LifecycleOpts BEFORE marshaling — opts.ParentExec/
	// ParentNode are live interface/pointer fields that cannot cross the []byte wire (R10 bed
	// finding: marshaling the raw EmitOpts crashed candy/plugin-bundle's decode the moment a
	// nested-child deploy's composed NestedExecutor made ParentExec non-nil). See
	// spec.LifecycleOptsFromEmit's doc comment.
	optsJSON, err := json.Marshal(spec.LifecycleOptsFromEmit(opts))
	if err != nil {
		return fmt.Errorf("external deploy %q: marshal opts: %w", t.name, err)
	}

	reply, err := t.dispatch(ctx, spec.DeployTargetDispatchRequest{
		Op: "add", Dir: dir, PlansJSON: plansJSON, OptsJSON: optsJSON, DistroCfgJSON: t.distroCfgJSON(),
		VenueJSON: t.applyParentExecOverride(opts),
	})
	if err != nil {
		return err
	}
	if opts.DryRun {
		return nil
	}

	artifactEnv := deploykit.BuildArtifactEnv(secretEnv, t.node)
	artifactKey := t.name
	if reply.ArtifactKey != "" {
		artifactKey = reply.ArtifactKey
	}
	if err := retrieveArtifactsAndK3s(ctx, t.venueExecutor(), candyList, artifactKey, t.name, artifactEnv, opts); err != nil {
		return fmt.Errorf("external deploy %q: retrieving candy artifacts: %w", t.name, err)
	}

	if opts.Verify {
		if t.hasLifecycle {
			fmt.Fprintf(os.Stderr, "external deploy %q: --verify deferred to `charly check live` (the %s substrate verifies its live venue post-deploy, with the venue's runtime identity)\n", t.name, t.word)
		} else {
			fails, verr := checkLocalDeployScope(dir, t.node, t.name, "", "", nil, t.venueExecutor(), "text")
			if verr != nil {
				return fmt.Errorf("external deploy %q: --verify: %w", t.name, verr)
			}
			if fails > 0 {
				return fmt.Errorf("external deploy %q: --verify: %d deploy-scope check(s) failed", t.name, fails)
			}
		}
	}
	return nil
}

// venueExecutor re-materializes the CURRENT venue (post-Add, whatever the plugin reported back)
// for core-side steps that need a live executor (retrieveArtifactsAndK3s, --verify). Falls back to
// t.exec (the initial placeholder) if no venue has been reported yet (e.g. a dry-run Add).
func (t *pluginDeployTarget) venueExecutor() deploykit.DeployExecutor {
	if len(t.venueJSON) == 0 {
		return t.exec
	}
	var d spec.VenueDescriptor
	if err := json.Unmarshal(t.venueJSON, &d); err != nil {
		return t.exec
	}
	exec, err := kit.VenueFromDescriptor(d)
	if err != nil || exec == nil {
		return t.exec
	}
	return exec
}

func (t *pluginDeployTarget) Update(ctx context.Context, plans []*deploykit.InstallPlan, opts UpdateOpts) error {
	views := make([]spec.InstallPlanView, 0, len(plans))
	for _, p := range plans {
		if p != nil {
			views = append(views, deploykit.WireView(p))
		}
	}
	plansJSON, err := json.Marshal(views)
	if err != nil {
		return fmt.Errorf("external deploy %q: marshal plans: %w", t.name, err)
	}
	// Marshals the SAME spec.LifecycleOpts shape Add() does (R3 — one wire shape for the shared
	// handleDeployApply body both dispatch through, mirroring the pre-move former core-resident
	// deploy target's Update, which built a plain deploykit.EmitOpts from these exact 5 fields and passed it into
	// the SAME shared apply() body Add used, rather than a separate wire shape).
	// RebuildImage is NEVER read by the apply body — it belongs to Rebuild's own
	// spec.DeployTargetRebuildOpts — so it is deliberately NOT threaded here.
	optsJSON, err := json.Marshal(spec.LifecycleOpts{
		DryRun: opts.DryRun, AssumeYes: opts.AssumeYes,
		AllowRepoChanges: opts.AllowRepoChanges, AllowRootTasks: opts.AllowRootTasks, WithServices: opts.WithServices,
	})
	if err != nil {
		return err
	}
	_, err = t.dispatch(ctx, spec.DeployTargetDispatchRequest{Op: "update", PlansJSON: plansJSON, OptsJSON: optsJSON, DistroCfgJSON: t.distroCfgJSON()})
	return err
}

// Test runs the deploy-scope checks against the host venue. The plugin is NOT involved — the
// checks are in-proc CheckVerbProviders run against the CURRENT venue, the SAME
// runUnifiedTargetChecks path the host/pod/vm targets use (R3).
func (t *pluginDeployTarget) Test(ctx context.Context, checks []spec.Op, opts TestOpts) error {
	return runUnifiedTargetChecks(ctx, t.venueExecutor(), t.Kind(), t.name, checks, opts)
}

func (t *pluginDeployTarget) Del(ctx context.Context, opts DelOpts) error {
	// Host-side substrate cleanup the plugin cannot do (vm: ephemeral-lifecycle teardown —
	// systemd timers + libvirt snapshot refcounts). Consulted GENERICALLY by word (pod registers
	// none). Runs BEFORE the plugin's own teardown, mirroring the pre-S3b PostTeardown ordering
	// exactly (the hook ran before the substrate's OpPostTeardown Invoke).
	if hook, ok := lifecyclePostTeardownHookFor(t.word); ok {
		if herr := hook(t.name, t.node); herr != nil {
			fmt.Fprintf(os.Stderr, "warning: substrate %q post-teardown host hook: %v\n", t.word, herr)
		}
	}
	optsJSON, err := json.Marshal(spec.DeployTargetDelOpts{
		DryRun: opts.DryRun, AssumeYes: opts.AssumeYes, KeepLedger: opts.KeepLedger, RemoveVolumes: opts.RemoveVolumes,
		KeepRepoChanges: t.KeepRepoChanges, KeepServices: t.KeepServices, KeepImage: t.KeepImage,
	})
	if err != nil {
		return err
	}
	_, err = t.dispatch(ctx, spec.DeployTargetDispatchRequest{Op: "del", OptsJSON: optsJSON})
	return err
}

func (t *pluginDeployTarget) Rebuild(ctx context.Context, opts RebuildOpts) error {
	optsJSON, err := json.Marshal(spec.DeployTargetRebuildOpts{RebuildImage: opts.RebuildImage, AssumeYes: opts.AssumeYes, DryRun: opts.DryRun})
	if err != nil {
		return err
	}
	_, err = t.dispatch(ctx, spec.DeployTargetDispatchRequest{Op: "rebuild", OptsJSON: optsJSON})
	return err
}

func (t *pluginDeployTarget) Status(ctx context.Context) (StatusInfo, error) {
	reply, err := t.dispatch(ctx, spec.DeployTargetDispatchRequest{Op: "status"})
	if err != nil {
		return StatusInfo{}, err
	}
	return StatusInfo{State: reply.Status.State, Healthy: reply.Status.Healthy, Details: reply.Status.Details}, nil
}

// Start dispatches OpStart. When this substrate registers a Start plan hook (today only "pod" —
// vm shells `charly vm start` and manages its own claim) the request carries HasPlan=true, so
// command:bundle's handleLifecycleSimple brackets its OWN dispatch with the Q1 resource-arbiter
// claim (the arbiter-bracket-acquire/-release HostBuild seams, host_build_arbiter_bracket.go) —
// FLOOR-SLIM-proper Unit-8's K4-exit: core no longer brackets the dispatch call itself (the
// former arbiter_bracket.go). Calls the ACTUAL registered lifecycleStartPlanHooks[t.word] closure
// (pod_lifecycle_dispatch.go, unmoved) rather than re-deriving its shape here — one source of
// truth for what the hook produces, R3.
func (t *pluginDeployTarget) Start(ctx context.Context) error {
	if !t.hasLifecycle {
		return fmt.Errorf("external deploy %q: %w", t.name, ErrNotSupportedOnExternal)
	}
	planHook, hasPlan := lifecycleStartPlanHooks[t.word]
	var optsJSON json.RawMessage
	if hasPlan {
		box, instance := deploykit.ParseDeployKey(t.name)
		var err error
		optsJSON, err = planHook(ctx, box, instance)
		if err != nil {
			return err
		}
	}
	_, err := t.dispatch(ctx, spec.DeployTargetDispatchRequest{Op: "start", OptsJSON: optsJSON, HasPlan: hasPlan})
	return err
}

// Stop mirrors Start — dispatches OpStop, HasPlan-flagged the same way when this substrate
// registers a Stop plan hook, calling the ACTUAL registered lifecycleStopPlanHooks[t.word] closure.
func (t *pluginDeployTarget) Stop(ctx context.Context) error {
	if !t.hasLifecycle {
		return fmt.Errorf("external deploy %q: %w", t.name, ErrNotSupportedOnExternal)
	}
	planHook, hasPlan := lifecycleStopPlanHooks[t.word]
	var optsJSON json.RawMessage
	if hasPlan {
		box, instance := deploykit.ParseDeployKey(t.name)
		var err error
		optsJSON, err = planHook(ctx, box, instance)
		if err != nil {
			return err
		}
	}
	_, err := t.dispatch(ctx, spec.DeployTargetDispatchRequest{Op: "stop", OptsJSON: optsJSON, HasPlan: hasPlan})
	return err
}

func (t *pluginDeployTarget) Logs(ctx context.Context, opts LogsOpts) error {
	if !t.hasLifecycle {
		return fmt.Errorf("external deploy %q: %w", t.name, ErrNotSupportedOnExternal)
	}
	optsJSON, err := json.Marshal(spec.DeployTargetLogsOpts{Follow: opts.Follow, Tail: int64(opts.Tail), Sidecar: opts.Sidecar})
	if err != nil {
		return err
	}
	_, err = t.dispatch(ctx, spec.DeployTargetDispatchRequest{Op: "logs", OptsJSON: optsJSON})
	return err
}

func (t *pluginDeployTarget) Shell(ctx context.Context, cmd []string) error {
	if !t.hasLifecycle {
		return fmt.Errorf("external deploy %q: %w", t.name, ErrNotSupportedOnExternal)
	}
	reply, err := t.dispatch(ctx, spec.DeployTargetDispatchRequest{Op: "shell", Cmd: cmd})
	if err != nil {
		return err
	}
	if reply.Output != "" {
		fmt.Print(reply.Output)
	}
	if reply.ExitCode != 0 {
		return &sdk.ExitCodeError{Code: int(reply.ExitCode)}
	}
	return nil
}

// Attach mirrors the pre-S3b former core-resident substrate lifecycle proxy's Attach exactly: a substrate registers an
// attachPlanResolver (today only "pod", via pod_lifecycle_dispatch.go, and "vm", via
// vm_lifecycle_preresolve.go's vmAttachResolver) — its ABSENCE is a hard error ("interactive
// attach not supported"), never a silent fallback, since a hookless substrate has no way to
// resolve the in-venue command. The resolved plan JSON (spec.PodAttachOpts for pod,
// spec.PodLiveStdioPlan for vm) rides as OptsJSON; the plugin threads it under the substrate's
// OWN expected "plan" key (handleDeployExec, candy/plugin-bundle/deploy_target.go).
func (t *pluginDeployTarget) Attach(ctx context.Context, cmd []string, tty bool) error {
	if !t.hasLifecycle {
		return fmt.Errorf("external deploy %q: %w", t.name, ErrNotSupportedOnExternal)
	}
	planHook, ok := lifecycleAttachPlanHooks[t.word]
	if !ok {
		return fmt.Errorf("substrate %q: interactive attach not supported", t.word)
	}
	box, instance := deploykit.ParseDeployKey(t.name)
	planJSON, err := planHook(ctx, box, instance, cmd, tty)
	if err != nil {
		return err
	}
	reply, err := t.dispatch(ctx, spec.DeployTargetDispatchRequest{Op: "attach", Cmd: cmd, TTY: tty, OptsJSON: planJSON})
	if err != nil {
		return err
	}
	if reply.ExitCode != 0 {
		return &sdk.ExitCodeError{Code: int(reply.ExitCode)}
	}
	return nil
}

// ErrNotSupportedOnExternal is returned by lifecycle methods that have no meaning for a hookless
// external deploy target (local/android/k8s carry no charly-owned runtime). Mirrors
// ErrNotSupportedOnHost.
var ErrNotSupportedOnExternal = fmt.Errorf("lifecycle operation not supported on external deploy target")

// ---------------------------------------------------------------------------
// ResolveTarget — the unified dispatcher.
// ---------------------------------------------------------------------------

// ResolveTarget returns the UnifiedDeployTarget for `name`, dispatching on the node's canonical
// target. The node MUST be the dispatch-merged BundleNode (project+operator field merge from
// resolveTreeRoot) — the adapter consumes node fields (Nested/Env/ephemeral/disposable) directly
// and NEVER re-reads them from disk.
//
// Errors:
//   - "no deployment X" — node absent / nil
//   - "X: missing required `target:`" — schema violation
//   - "X: unknown target Y" — Y is not a canonical substrate word (a typo)
//   - "X: target Y is a known substrate but its deploy provider is not connected" — Y is valid but
//     its out-of-process plugin is not compiled-in / failed to load (unresolvedDeployTargetError)
func ResolveTarget(node *spec.BundleNode, name string) (UnifiedDeployTarget, error) {
	if node == nil {
		return nil, fmt.Errorf("no deployment %q; run `charly bundle list`", name)
	}
	if node.Target == "" {
		return nil, fmt.Errorf("deployment %q missing required `target:` field "+
			"(local|vm|pod|k8s|android)", name)
	}
	prov, ok := providerRegistry.ResolveDeploy(node.Target)
	if !ok {
		return nil, unresolvedDeployTargetError(name, node.Target)
	}
	if dp, ok := prov.(DeployTargetProvider); ok {
		return dp.ResolveTarget(node, name)
	}
	// An OUT-OF-PROCESS deploy provider (a grpcProvider, Invoke-only) drives the deploy lifecycle
	// via candy/plugin-bundle's Invoke(OpDeployDispatch) — S3b. The executor is chosen by the
	// node's host: field via the SHARED selector rootExecutorForDeployNode (R3): ShellExecutor
	// for host:local/absent, SSHExecutor for host:user@machine.
	gp, ok := prov.(*grpcProvider)
	if !ok {
		return nil, fmt.Errorf("deployment %q: target %q has no in-process resolver and is not an out-of-proc plugin provider", name, node.Target)
	}
	exec, perr := deploykit.RootExecutorForDeployNode(node)
	if perr != nil {
		return nil, fmt.Errorf("deployment %q: %w", name, perr)
	}
	return &pluginDeployTarget{
		name: name, word: gp.word, hasLifecycle: gp.lifecycle, hasPreresolve: gp.preresolve,
		node: node, exec: exec,
	}, nil
}

// unresolvedDeployTargetError distinguishes the two ways ResolveDeploy(target) can fail: a
// genuinely UNKNOWN target word (a typo — not one of the canonical substrates), versus a KNOWN
// substrate word whose deploy provider is merely NOT CONNECTED (the deploy-substrate plugins are
// out-of-process and connected on demand via loadDeployPlugins — a target with no charly.yml entry,
// or a plugin that failed to load, leaves the registry entry absent). The former is a user error;
// the latter is a load/connect gap. Conflating them (the former "unknown target" text for both)
// misdirected the operator during the check-k3s-vm RCA.
func unresolvedDeployTargetError(name, target string) error {
	if externalizedDeploySubstrates[target] {
		return fmt.Errorf("deployment %q: target %q is a known substrate but its deploy provider "+
			"is not connected (the %s plugin candy is not compiled-in or failed to load)",
			name, target, externalDeploySubstratePlugins[target])
	}
	return fmt.Errorf("deployment %q: unknown target %q (want local|vm|pod|k8s|android)", name, target)
}

// compile-time assertion: the plugin-dispatch adapter satisfies the interfaces it claims. If any
// method signature drifts, `go build` fails here.
var (
	_ UnifiedDeployTarget = (*pluginDeployTarget)(nil)
	_ LifecycleTarget     = (*pluginDeployTarget)(nil)
)
