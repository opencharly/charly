package main

// deploy_target_unified.go — the canonical DeployTarget interface.
//
// The legacy DeployTarget interface in install_plan.go is the 2-method
// contract (Name + Emit) that the retained BUILD ENGINES (OCITarget, PodDeployTarget)
// satisfy at the IR-emission level. This file defines the lifecycle-and-management
// contract layered on top: UnifiedDeployTarget with the per-verb methods, plus
// LifecycleTarget for the live-runtime targets.
//
// Every `charly bundle add` / `charly bundle del` / `charly update` dispatches through
// ResolveTarget (unified_targets.go) → an UnifiedDeployTarget adapter. ALL FIVE substrates
// (local/vm/pod/k8s/android) are EXTERNAL — each resolves to the generic externalDeployTarget
// over the executor reverse channel, served by its own out-of-process plugin. The core build
// engines they once wrapped (PodDeployTarget overlay synthesis; the VM disk build) are now
// invoked HOST-SIDE from each substrate's registered substrateLifecycle hook (pod/vm) or
// preresolver (android/k8s). There is no per-kind dispatch switch in the cmd files — the kind
// lives behind the adapter method.

import (
	"context"
)

// DeployContext carries everything an Add needs from the generic
// dispatchNode pre-stage: the dispatch-merged BundleNode (the
// project+operator field-level merge from resolveTreeRoot — the SINGLE
// source of truth for node fields like Nested/Env/ephemeral/disposable,
// NEVER re-read via loadDeployConfigForRead inside an Add), the deploy
// name + project dir, the loaded image/distro/builder configs, and the
// resolved primary base ref. One value threaded into every target.Add so
// each adapter constructs its live embedded target without re-resolving
// config that dispatchNode already loaded.
type DeployContext struct {
	// Node is the dispatch-merged BundleNode. nil for a ref-based
	// deploy with no charly.yml entry (e.g. `charly bundle add host ./x.yml`).
	Node *BundleNode

	// Name is the deploy key (the bed key / charly.yml map key, e.g.
	// "check-k3s-vm"). Distinct from the kind:vm entity name (node.From).
	Name string

	// Dir is the project directory.
	Dir string

	// Cfg / DistroCfg / BuilderCfg are the configs loaded once by
	// dispatchNode (loadConfigForDeploy). Reused by each Add so the
	// construction matches what dispatchNode compiled plans against.
	Cfg        *Config
	DistroCfg  *DistroConfig
	BuilderCfg *BuilderConfig

	// Base is the resolved primary base — the image name for pod/k8s,
	// or the deploy path for target-only kinds (local/vm/android).
	Base string
}

// UnifiedDeployTarget is the unified contract all four deploy methods
// (host, vm, pod, k8s) implement uniformly. Each method corresponds to
// an `charly bundle …` subcommand, so the dispatcher in resolve_target.go
// can route purely on target.Kind() without per-cmd switches.
type UnifiedDeployTarget interface {
	// Name is the deployment's identifier from charly.yml (e.g.
	// "arch-vm", "sway-pod"). Unique within a charly.yml.
	Name() string

	// Kind returns one of "host" | "vm" | "pod" | "k8s".
	// Drives ledger keying ("<kind>:<name>") and command dispatch.
	Kind() string

	// Executor returns the DeployExecutor this target will use for
	// shell operations. For host → ShellExecutor; for vm →
	// SSHExecutor; for pod → a podman-exec wrapper; for k8s → a nop
	// executor that errors on invocation (k8s operates via
	// kubectl/Kustomize, not shell ops).
	//
	// Exposing the executor on the interface lets parent targets in
	// a nested tree compose a NestedExecutor over the child. This
	// is also the sole plumbing point for the `inside:` cross-ref
	// (a target=host deployment with inside: arch-vm resolves its
	// executor as NestedExecutor(ResolveTarget(arch-vm).Executor())).
	Executor() DeployExecutor

	// Add applies the given plans to the target. Equivalent to
	// `charly bundle add <name>`. Idempotent: re-applying the same plan
	// is safe. dctx carries the dispatch-merged node + loaded configs;
	// the adapter constructs its live embedded target from it (never
	// re-reading the node from disk — see DeployContext).
	Add(ctx context.Context, dctx *DeployContext, plans []*InstallPlan, opts EmitOpts) error

	// Del reverses every candy currently recorded for this target
	// and removes the deploy record. Equivalent to `charly bundle del
	// <name>`. Only recorded ReverseOps are replayed — never an
	// ad-hoc computation from the candy manifest.
	Del(ctx context.Context, opts DelOpts) error

	// Test runs the given deploy-scope checks against the live
	// target. Equivalent to `charly check live <name>`. Returns nil only if
	// every non-skipped check passes.
	Test(ctx context.Context, checks []Op, opts TestOpts) error

	// Update re-applies the plan diff between the currently-recorded
	// candy set and the plan set derived from fresh charly.yml.
	// Equivalent to `charly bundle update <name>` (new command; today's
	// `charly update` is image-focused and will be separate).
	Update(ctx context.Context, plans []*InstallPlan, opts UpdateOpts) error
}

// LifecycleTarget extends UnifiedDeployTarget for live-runtime targets
// (host, vm, pod). K8s does NOT implement this: its cluster lifecycle
// is kubectl-managed outside charly. Commands that require a live runtime
// (charly start/stop/status/logs/shell/rebuild) assert the interface and
// error uniformly on k8s targets.
type LifecycleTarget interface {
	UnifiedDeployTarget

	// Start brings the target up (charly start / podman start / virsh
	// start / systemctl start as appropriate). Idempotent: no-op if
	// already running.
	Start(ctx context.Context) error

	// Stop brings the target down. Idempotent.
	Stop(ctx context.Context) error

	// Status reports the target's live runtime state.
	Status(ctx context.Context) (StatusInfo, error)

	// Logs streams or tails the target's logs. See LogsOpts for
	// follow/tail semantics.
	Logs(ctx context.Context, opts LogsOpts) error

	// Shell opens an interactive shell in the target. cmd, if
	// non-empty, is run instead of starting a login shell.
	Shell(ctx context.Context, cmd []string) error

	// Attach runs an INTERACTIVE or live-stdio session on the target's venue,
	// wired to the operator's terminal (F12). It is the `charly shell` / `charly
	// cmd` leg — distinct from Shell (the #57 `charly service` NON-interactive
	// capture leg). tty selects the resolver + PTY policy: tty=true is `charly
	// shell` (a `-it` TTY, with an ephemeral-run fallback for a stopped pod);
	// tty=false is `charly cmd` (a `-i` inherited-stdin exec into the running
	// container). cmd is the command argv (empty ⇒ an interactive login shell).
	// The host resolves the venue-local command; the owning plugin runs it over
	// the served venue executor via RunInteractive (stdio stays host-side). A
	// non-zero exit is propagated as *sdk.ExitCodeError.
	Attach(ctx context.Context, cmd []string, tty bool) error

	// Rebuild is destroy + create + start. Gated on the target's
	// Disposable flag — each implementation must verify it before
	// any destructive action. See /charly-internals:disposable for the
	// authorization contract.
	Rebuild(ctx context.Context, opts RebuildOpts) error
}

// ---------------------------------------------------------------------------
// Opts types — one per method.
// ---------------------------------------------------------------------------

// DelOpts parameterizes `charly bundle del`.
type DelOpts struct {
	// DryRun prints what would happen without executing.
	DryRun bool

	// AssumeYes skips confirmation prompts.
	AssumeYes bool

	// KeepLedger retains the ledger records after reversal. Useful
	// for debugging a failed teardown.
	KeepLedger bool

	// RemoveVolumes deletes bind-mount / named-volume data. Off by
	// default to avoid accidental data loss.
	RemoveVolumes bool
}

// TestOpts parameterizes `charly check live` against a live deployment.
type TestOpts struct {
	// OnlyIDs restricts the run to the listed check IDs. Empty =
	// run every check defined on the deployment.
	OnlyIDs []string

	// FormatJSON emits machine-readable output instead of the human
	// summary table.
	FormatJSON bool

	// StopOnFail aborts on the first failing check.
	StopOnFail bool
}

// UpdateOpts parameterizes `charly bundle update`.
type UpdateOpts struct {
	DryRun           bool
	AssumeYes        bool
	RebuildImage     bool
	AllowRepoChanges bool
	AllowRootTasks   bool
	WithServices     bool
}

// StatusInfo summarizes a live deployment's runtime state.
type StatusInfo struct {
	// State is one of "running" | "stopped" | "paused" | "crashed"
	// | "unknown". Implementations normalize the underlying
	// runtime's lingo into this set.
	State string

	// Healthy combines runtime state + health-probe result, if any.
	Healthy bool

	// Details is a free-form map for target-specific extras (PID,
	// VM instance-id, cgroup, container image ref, etc.). Keys and
	// values are strings for ergonomic printing; values that are
	// semantically structured should marshal through JSON.
	Details map[string]string
}

// LogsOpts parameterizes `charly logs`.
type LogsOpts struct {
	// Follow streams new log lines as they arrive (tail -f style).
	Follow bool

	// Tail is the number of trailing lines to emit first. 0 = all.
	Tail int

	// Sidecar, when set, targets the named sidecar container's journal
	// instead of the app container's (`charly logs --sidecar <name>`).
	Sidecar string
}

// RebuildOpts parameterizes the rebuild path of `charly update`. Per /charly-internals:disposable, the
// target MUST be marked `disposable: true` in charly.yml — every
// implementation asserts this before any destructive action.
type RebuildOpts struct {
	// RebuildImage forces an `charly box build` before redeploy. Off by
	// default: rebuilds only the deployment, reusing the existing
	// image ref.
	RebuildImage bool

	// AssumeYes skips confirmation prompts (already-gated by the
	// disposable check; this covers any additional "really rebuild?"
	// prompts from the underlying runtime).
	AssumeYes bool

	// DryRun prints the destroy + create steps without executing.
	DryRun bool
}
