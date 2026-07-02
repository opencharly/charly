package spec

// arbiter_wire.go — the RESOURCE-ARBITER wire types shared between charly's core
// (package main) and the COMPILED-IN candy/plugin-preempt (cutover C9).
//
// These types live in package spec — the ONE importable home — because BOTH the
// host (the in-core PROXY + the 7 arbiter host-seams over the reverse channel,
// preempt.go) AND the plugin (candy/plugin-preempt's verb:arbiter provider, via the
// replace → ../../charly module edge) construct and exchange them across the
// verb:arbiter Invoke boundary AND the ExecutorService.HostArbiter reverse channel.
//
// The arbiter LOGIC (AcquireExclusive/AcquireShared/ReleaseClaimant/stopHolders/
// restoreHolders/reconcileStranded/the lease ledger/poison/mode-math) lives in the
// plugin and operates on these spec types; the arbiter's host DEPENDENCIES (config
// read, VM/pod lifecycle, the GPU driver flip, readiness) stay host-side and are
// reached mid-logic via the HostArbiter reverse channel. There is NO duplicate type
// for any of these concepts (R3).

// --- canonical preemption policy values (shared: the host Gather projection
// produces Restore, the plugin's releaseLeaseEffects reads it) ------------------

const (
	// PreemptStopShutdown is the only supported stop mechanism (graceful ACPI
	// shutdown / podman stop; disk preserved).
	PreemptStopShutdown = "shutdown"
	// PreemptRestoreAlways restarts the holder regardless of the claim's outcome
	// (the default).
	PreemptRestoreAlways = "always"
	// PreemptRestoreSuccess restarts the holder only if the claim released cleanly;
	// on a failed claim it is left stopped for operator inspection.
	PreemptRestoreSuccess = "on-success"
)

// --- persisted arbiter state (the lease ledger; the plugin does the yaml I/O) --

// HolderAddr is the self-contained address of a deployment (holder or claimant) —
// enough for the host seams to probe/stop/start it WITHOUT re-reading config, so a
// lease loaded after a crash can act on it. Carries BOTH yaml (the persisted
// ledger) AND json (the Invoke/reverse-channel envelope) tags.
type HolderAddr struct {
	Name     string `yaml:"name" json:"name"`                             // full deploy key (for messages)
	Target   string `yaml:"target" json:"target"`                         // "vm" | "pod"
	Base     string `yaml:"base" json:"base"`                             // parseDeployKey base (pod container basis / vm fallback)
	Instance string `yaml:"instance,omitempty" json:"instance,omitempty"` // parseDeployKey instance
	Vm       string `yaml:"vm,omitempty" json:"vm,omitempty"`             // vm entity (target:vm)
}

// PreemptedHolder records one holder a lease stopped, its declared exclusive
// tokens, and its restore policy — so ReleaseClaimant/reconcile restart exactly
// what was stopped.
type PreemptedHolder struct {
	Addr    HolderAddr `yaml:"addr" json:"addr"`
	Holds   []string   `yaml:"holds" json:"holds"`
	Restore string     `yaml:"restore" json:"restore"` // always | on-success
}

// PreemptLease is one active resource claim — exclusive (a VM with sole use) OR
// shared (a refcounted pod claim; many coexist on one token).
type PreemptLease struct {
	Claimant  string            `yaml:"claimant" json:"claimant"`
	Claim     HolderAddr        `yaml:"claim" json:"claim"` // the claimant DEPLOYMENT addr (persistent-lease liveness)
	Tokens    []string          `yaml:"tokens" json:"tokens"`
	Shared    bool              `yaml:"shared,omitempty" json:"shared,omitempty"` // true = refcounted SHARED (pods); false = EXCLUSIVE (VM)
	Mode      string            `yaml:"mode,omitempty" json:"mode,omitempty"`     // driver MODE: "nvidia" (shared) | "vfio" (exclusive); "" = legacy/none
	Transient bool              `yaml:"transient" json:"transient"`               // check-bed claims auto-release; persistent claims don't
	Preempted []PreemptedHolder `yaml:"preempted" json:"preempted"`               // holders/pods THIS claim stopped + must restore on release
	Created   string            `yaml:"created" json:"created"`                   // RFC3339 UTC
	// OwnerPID/OwnerStart identify the OUTERMOST process that created the lease —
	// the liveness signal a concurrent charly process's reconcile uses (leaseLive).
	OwnerPID   int    `yaml:"owner_pid,omitempty" json:"owner_pid,omitempty"`
	OwnerStart string `yaml:"owner_start,omitempty" json:"owner_start,omitempty"`
}

// PreemptLedger is the on-disk lease set (~/.local/share/charly/preemption/leases.yml).
type PreemptLedger struct {
	Leases []PreemptLease `yaml:"leases" json:"leases"`
}

// --- HostArbiter reverse-channel seams (host serves, plugin calls mid-logic) ---
//
// One action-multiplexed RPC (ExecutorService.HostArbiter) carries all seams, the
// SAME pattern as the C11 GpuProbeInput action multiplex. The plugin sends an
// ArbiterHost* request tagged by ArbiterAction*; the host runs the seam's CURRENT
// default implementation (gatherPreemptibleHolders/gatherResources/holderRunning/
// holderStop+waitStopped/holderStart/gpuSwitchModeTolerant/ensureCDIRoot) and
// replies. The stop seam FOLDS the wait-until-stopped (it keeps the host-side
// readiness StopGate + pollUntil, so no readiness machinery moves into the plugin).

const (
	ArbiterSeamGather    = "gather"     // -> ArbiterGatherReply (preemptible holders, projected)
	ArbiterSeamResources = "resources"  // -> ArbiterResourcesReply (gpu-backed tokens -> vendor)
	ArbiterSeamRunning   = "running"    // ArbiterHolderReq -> ArbiterBoolReply
	ArbiterSeamStop      = "stop"       // ArbiterHolderReq -> ArbiterErrReply (stop + wait-until-stopped)
	ArbiterSeamStart     = "start"      // ArbiterHolderReq -> ArbiterErrReply
	ArbiterSeamSwitch    = "switchMode" // ArbiterSwitchReq -> ArbiterErrReply (routes to plugin-gpu)
	ArbiterSeamEnsureCDI = "ensureCDI"  // (no payload) -> (no reply)
)

// ArbiterGatherReply is the host's projection of every RUNNING-or-not preemptible
// holder the arbiter may stop: PreemptionHolds() + holderAddrFor() +
// preemptEffectiveRestore() are applied host-side so the plugin's holdersToStop is
// pure config-free coordination.
type ArbiterGatherReply struct {
	Holders []HolderDescriptor `json:"holders"`
}

// HolderDescriptor is one candidate preemptible holder, pre-projected host-side.
type HolderDescriptor struct {
	Name    string     `json:"name"`
	Holds   []string   `json:"holds"`
	Addr    HolderAddr `json:"addr"`
	Restore string     `json:"restore"`
}

// ArbiterResourcesReply maps each GPU-BACKED arbitration token to its PCI vendor
// (e.g. "nvidia-gpu" -> "0x10de"). A token ABSENT from the map is arbitration-only
// (no device to flip; applyMode skips it, firstPoisonedToken ignores it).
type ArbiterResourcesReply struct {
	Gpu map[string]string `json:"gpu"`
}

// ArbiterHolderReq addresses one deployment for the running/stop/start seams.
type ArbiterHolderReq struct {
	Addr HolderAddr `json:"addr"`
}

// ArbiterSwitchReq flips a GPU-backed token's whole IOMMU group to a driver mode.
type ArbiterSwitchReq struct {
	Vendor string `json:"vendor"`
	Mode   string `json:"mode"`
}

// ArbiterBoolReply is the running-seam reply.
type ArbiterBoolReply struct {
	Bool bool `json:"bool"`
}

// ArbiterErrReply carries a seam's operation error ("" = success) — the
// reverse-channel convention (the RPC itself succeeds; the op error rides here).
type ArbiterErrReply struct {
	Error string `json:"error,omitempty"`
}

// ArbiterSwitchReply is the switchMode-seam reply: the op error plus whether the flip
// WEDGED the device_lock (the sentinel can't cross the process boundary, so the bool
// carries it — the plugin's applyMode poisons the resource on Wedged).
type ArbiterSwitchReply struct {
	Error  string `json:"error,omitempty"`
	Wedged bool   `json:"wedged,omitempty"`
}

// --- verb:arbiter Invoke actions (the in-core PROXY -> the plugin) -------------
//
// The in-core proxy resolves verb:arbiter and Invokes OpRun with an action-tagged
// ArbiterInvokeInput; the plugin runs the arbiter method and returns the matching
// reply. Mirrors gpu_shim's resolve+Invoke of verb:gpu.

const (
	ArbiterActionAcquireExclusive = "acquire-exclusive"
	ArbiterActionAcquireShared    = "acquire-shared"
	ArbiterActionRelease          = "release-claimant"
	ArbiterActionStatus           = "status"
	ArbiterActionReconcile        = "reconcile-stranded"
	ArbiterActionClearPoison      = "clear-poison"
	ArbiterActionResourcePoisoned = "resource-poisoned"
)

// ArbiterInvokeInput is the action-multiplexed input the proxy ships to
// verb:arbiter. Each action populates only the field(s) it needs.
type ArbiterInvokeInput struct {
	Action    string     `json:"action"`
	Claimant  string     `json:"claimant,omitempty"`
	Tokens    []string   `json:"tokens,omitempty"`
	ClaimAddr HolderAddr `json:"claim_addr,omitempty"`
	Transient bool       `json:"transient,omitempty"`
	Success   bool       `json:"success,omitempty"` // release-claimant
	Token     string     `json:"token,omitempty"`   // clear-poison / resource-poisoned
}

// ArbiterInvokeReply is the action-multiplexed reply from verb:arbiter.
type ArbiterInvokeReply struct {
	Active   bool           `json:"active,omitempty"`   // acquire-* : the lease is active (env must be marked)
	Bool     bool           `json:"bool,omitempty"`     // resource-poisoned
	Ledger   *PreemptLedger `json:"ledger,omitempty"`   // status
	Stranded []string       `json:"stranded,omitempty"` // status
	Error    string         `json:"error,omitempty"`
}
