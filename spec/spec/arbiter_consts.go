package spec

// arbiter_consts.go — the RESOURCE-ARBITER string-enum constants shared between charly's core and
// the COMPILED-IN candy/plugin-preempt (cutover C9; FLOOR-SLIM-proper Unit-8B SDD conversion).
// These are NOT wire-type structs — plain named string literals never carry a JSON/YAML shape for
// `cue exp gengotypes` to generate — so they stay hand-written here, mirroring the existing
// spec/gpu_consts.go precedent (a small hand-written consts file living beside its CUE-generated
// struct siblings, spec/cue_types_gen.go's #HolderAddr/#PreemptLease/#ArbiterInvokeInput/…). The
// struct types this file's consts tag (HolderAddr, PreemptedHolder, PreemptLease, PreemptLedger,
// HolderDescriptor, ArbiterInvokeInput, ArbiterInvokeReply) are CUE-sourced at
// sdk/schema/arbiter.cue → generated into cue_types_gen.go.
//
// K1-unblock wave 1 retired the ExecutorService.HostArbiter reverse RPC entirely: its last 2
// actions (gather/resources) now read the generic resolved-project envelope instead
// (candy/plugin-preempt/arbiter.go, fetched via InvokeProvider("build","project", OpResolve) — the
// former HostBuild("resolved-project") seam is DELETED — the same envelope every other
// resolved-project consumer uses) —
// so ArbiterSeamGather/ArbiterSeamResources and the #ArbiterGatherReply/#ArbiterResourcesReply
// wire types they tagged are DELETED (not merely unused), along with charly/arbiter_host.go.

// --- canonical preemption policy values (shared: the resolved-project-reading gather projection
// produces Restore, the plugin's releaseLeaseEffects reads it) -------------------------------

const (
	// PreemptStopShutdown is the only supported stop mechanism (graceful ACPI shutdown / podman
	// stop; disk preserved).
	PreemptStopShutdown = "shutdown"
	// PreemptRestoreAlways restarts the holder regardless of the claim's outcome (the default).
	PreemptRestoreAlways = "always"
	// PreemptRestoreSuccess restarts the holder only if the claim released cleanly; on a failed
	// claim it is left stopped for operator inspection.
	PreemptRestoreSuccess = "on-success"
)

// --- verb:arbiter Invoke actions (the in-core PROXY -> the plugin) ---------------------------
//
// The in-core proxy resolves verb:arbiter and Invokes OpRun with an action-tagged
// ArbiterInvokeInput; the plugin runs the arbiter method and returns the matching reply. Mirrors
// gpu_shim's resolve+Invoke of verb:gpu.

const (
	ArbiterActionAcquireExclusive = "acquire-exclusive"
	ArbiterActionAcquireShared    = "acquire-shared"
	ArbiterActionRelease          = "release-claimant"
	ArbiterActionStatus           = "status"
	ArbiterActionReconcile        = "reconcile-stranded"
	ArbiterActionClearPoison      = "clear-poison"
	ArbiterActionResourcePoisoned = "resource-poisoned"
)
