package spec

// deploy_target_unified.go — the kind-agnostic deploy-target contract.
//
// The legacy DeployTarget interface (install_plan.go) is the 2-method
// contract (Name + Emit) that the retained BUILD ENGINES (the pod-overlay
// walker, sdk/deploykit.OCITarget) satisfy at the IR-emission level. This
// file defines the lifecycle-and-management contract layered on top:
// UnifiedDeployTarget with the per-verb methods, plus LifecycleTarget for
// the live-runtime targets.
//
// The contract lives in spec (not core) because its implementor/consumer
// set spans the kernel/plugin boundary: every method maps to one op of the
// wire DeployTargetDispatchRequest (schema/seam.cue), dispatched by core's
// thin ResolveTarget proxy (charly/unified_targets.go) through
// command:fleet's Invoke(OpDeployDispatch). There is no per-kind dispatch
// switch in the cmd files — the kind lives behind the adapter method. The
// option structs whose shapes cross the wire (Del/Status/Logs/Rebuild) are
// the CUE-sourced DeployTarget* types; only the plain Go contracts below
// live here.

import "context"

// DeployContext carries everything an Add needs from the generic
// dispatchNode pre-stage: the dispatch-merged FleetNode (the
// project+operator field-level merged deploy tree — the SINGLE
// source of truth for node fields like Nested/Env/ephemeral/disposable,
// NEVER re-read inside an Add), the deploy name + project dir, the loaded
// image/distro/builder configs, and the resolved primary base ref. One
// value threaded into every target.Add so each adapter constructs its live
// embedded target without re-resolving config that dispatchNode already
// loaded.
type DeployContext struct {
	// Node is the dispatch-merged FleetNode. nil for a ref-based
	// deploy with no charly.yml entry (e.g. `charly fleet add host ./x.yml`).
	Node *FleetNode

	// Name is the deploy key (the bed key / charly.yml map key, e.g.
	// "check-k3s-vm"). Distinct from the kind:vm entity name (node.From).
	Name string

	// Dir is the project directory.
	Dir string

	// Cfg / DistroCfg / BuilderCfg are the configs loaded once by the
	// resolve-target-add host seam. Reused by each Add so the construction
	// matches what the plugin compiled plans against.
	Cfg        *Config
	DistroCfg  *DistroConfig
	BuilderCfg *BuilderConfig
}

// UnifiedDeployTarget is the unified contract all five deploy methods
// (local, vm, pod, kubernetes, android) implement uniformly. Each method corresponds
// to an `charly fleet …` subcommand, so the dispatcher in ResolveTarget
// (charly/unified_targets.go) can route purely on target.Kind() without
// per-cmd switches. Every method dispatches through the ONE generic
// DeployTargetDispatchRequest envelope, discriminated by an `op` field.
type UnifiedDeployTarget interface {
	// Name is the deployment's identifier from charly.yml (e.g.
	// "arch-vm", "sway-pod"). Unique within a charly.yml.
	Name() string

	// Kind returns one of "host" | "vm" | "pod" | "kubernetes".
	// Drives ledger keying ("<kind>:<name>") and command dispatch.
	Kind() string

	// Executor returns the DeployExecutor this target will use for
	// shell operations. For host → ShellExecutor; for vm →
	// SSHExecutor; for pod → a podman-exec wrapper; for kubernetes → a nop
	// executor that errors on invocation (kubernetes operates via
	// kubectl/Kustomize, not shell ops).
	//
	// Exposing the executor on the interface lets parent targets in
	// a nested tree compose a NestedExecutor over the child.
	Executor() DeployExecutor

	// Add applies the given plans to the target. Equivalent to
	// `charly fleet add <name>`. Idempotent: re-applying the same plan
	// is safe. dctx carries the dispatch-merged node + loaded configs;
	// the adapter constructs its live embedded target from it (never
	// re-reading the node from disk — see DeployContext).
	Add(ctx context.Context, dctx *DeployContext, plans []*InstallPlan, opts EmitOpts) error

	// Del reverses every candy currently recorded for this target
	// and removes the deploy record. Equivalent to `charly fleet del
	// <name>`. Only recorded ReverseOps are replayed — never an
	// ad-hoc computation from the candy manifest.
	Del(ctx context.Context, opts DeployTargetDelOpts) error

	// Update re-applies the plan diff between the currently-recorded
	// candy set and the plan set derived from fresh charly.yml.
	// Equivalent to `charly fleet update <name>` (new command; today's
	// `charly update` is image-focused and will be separate).
	Update(ctx context.Context, plans []*InstallPlan, opts UpdateOpts) error
}

// LifecycleTarget extends UnifiedDeployTarget for live-runtime targets
// (host, vm, pod). Kubernetes does NOT implement this: its cluster lifecycle
// is kubectl-managed outside charly. Commands that require a live runtime
// (charly start/stop/status/logs/shell/rebuild) assert the interface and
// error uniformly on kubernetes targets.
type LifecycleTarget interface {
	UnifiedDeployTarget

	// Start brings the target up (charly start / podman start / virsh
	// start / systemctl start as appropriate). Idempotent: no-op if
	// already running.
	Start(ctx context.Context) error

	// Stop brings the target down. Idempotent.
	Stop(ctx context.Context) error

	// Status reports the target's live runtime state.
	Status(ctx context.Context) (DeployTargetStatus, error)

	// Logs streams or tails the target's logs. See DeployTargetLogsOpts
	// for follow/tail semantics.
	Logs(ctx context.Context, opts DeployTargetLogsOpts) error

	// Shell opens an interactive shell in the target. cmd, if
	// non-empty, is run instead of starting a login shell.
	Shell(ctx context.Context, cmd []string) error

	// Attach runs an INTERACTIVE or live-stdio session on the target's venue,
	// wired to the operator's terminal. It is the `charly shell` / `charly
	// cmd` leg — distinct from Shell (the `charly service` NON-interactive
	// capture leg). tty selects the resolver + PTY policy: tty=true is
	// `charly shell` (a `-it` TTY, with an ephemeral-run fallback for a
	// stopped pod); tty=false is `charly cmd` (a `-i` inherited-stdin exec
	// into the running container). cmd is the command argv (empty ⇒ an
	// interactive login shell). The host resolves the venue-local command;
	// the owning plugin runs it over the served venue executor via
	// RunInteractive (stdio stays host-side). A non-zero exit is propagated
	// as a typed exit-code error.
	Attach(ctx context.Context, cmd []string, tty bool) error

	// Rebuild is destroy + create + start. Gated on the target's
	// Disposable flag — each implementation must verify it before
	// any destructive action.
	Rebuild(ctx context.Context, opts DeployTargetRebuildOpts) error
}

// UpdateOpts parameterizes `charly fleet update`. On the wire Update
// marshals the SAME spec.LifecycleOpts shape Add does (R3 — one wire shape
// for the shared apply body); RebuildImage is deliberately NOT threaded
// there — it belongs to DeployTargetRebuildOpts.
type UpdateOpts struct {
	DryRun           bool
	AssumeYes        bool
	RebuildImage     bool
	AllowRepoChanges bool
	AllowRootTasks   bool
	WithServices     bool
}
