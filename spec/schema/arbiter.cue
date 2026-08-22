// arbiter.cue — the RESOURCE-ARBITER wire + persisted-state types (FLOOR-SLIM-proper Unit-8B
// SDD conversion, per the standing operator directive: a wire type not previously CUE-sourced is
// no excuse to keep it hand-written in freshly committed code — WIRE TYPES ARE CUE-SOURCED
// WITHOUT EXCEPTION, CLAUDE.md SDD). These types are shared between charly's core (package main —
// the in-core PROXY) and the COMPILED-IN candy/plugin-preempt (verb:arbiter provider, via the
// replace → ../../charly module edge): the persisted lease-ledger state, and the verb:arbiter
// Invoke action-multiplexed input/reply. K1-unblock wave 1 retired the ExecutorService.HostArbiter
// reverse RPC (and its #ArbiterGatherReply/#ArbiterResourcesReply replies) — the arbiter's last 2
// host dependencies (gather/resources) now read the generic #ResolvedProject envelope instead
// (resolvedproject.cue), the same seam every other resolved-project consumer uses.
//
// None of these are open-tailed (no `{known?: string, {[string]: _}}` shape) — every field is
// fully typed and closed, so NONE qualify for the hand_state_types.go exception; they are plain
// seam/state structs and convert without exception. The small string-enum CONST groups this
// domain also carries (PreemptStopShutdown/PreemptRestoreAlways/PreemptRestoreSuccess, the 7
// ArbiterAction* names) are NOT wire-type structs — they stay hand-written in
// spec/arbiter_consts.go, mirroring the existing spec/gpu_consts.go precedent (a small
// hand-written consts file beside its CUE-generated struct siblings).

// #HolderAddr is the self-contained address of a deployment (holder or claimant) — enough for the
// host seams to probe/stop/start it WITHOUT re-reading config, so a lease loaded after a crash can
// act on it. Carries BOTH yaml (the persisted ledger) and json (the Invoke/reverse-channel
// envelope) tags — the retag step emits both automatically, mirroring the json key.
#HolderAddr: {
	name!:     string @go(Name)     // full deploy key (for messages)
	target!:   string @go(Target)   // "vm" | "pod"
	base!:     string @go(Base)     // parseDeployKey base (pod container basis / vm fallback)
	instance?: string @go(Instance) // parseDeployKey instance
	vm?:       string @go(Vm)       // vm entity (target:vm)
}

// #PreemptedHolder records one holder a lease stopped, its declared exclusive tokens, and its
// restore policy — so ReleaseClaimant/reconcile restart exactly what was stopped.
#PreemptedHolder: {
	addr!: #HolderAddr @go(Addr)
	holds!: [...string] @go(Holds)
	restore!: string @go(Restore) // always | on-success
}

// #PreemptLease is one active resource claim — exclusive (a VM with sole use) or shared (a
// refcounted pod claim; many coexist on one token).
#PreemptLease: {
	claimant!: string @go(Claimant)
	claim!:    #HolderAddr @go(Claim) // the claimant DEPLOYMENT addr (persistent-lease liveness)
	tokens!: [...string] @go(Tokens)
	shared?:   bool @go(Shared) // true = refcounted SHARED (pods); false = EXCLUSIVE (VM)
	mode?:     string @go(Mode) // driver MODE: "nvidia" (shared) | "vfio" (exclusive); "" = legacy/none
	transient!: bool @go(Transient) // check-bed claims auto-release; persistent claims don't
	preempted!: [...#PreemptedHolder] @go(Preempted) // holders/pods THIS claim stopped + must restore on release
	created!:  string @go(Created) // RFC3339 UTC
	// owner_pid/owner_start identify the OUTERMOST process that created the lease — the liveness
	// signal a concurrent charly process's reconcile uses (leaseLive).
	owner_pid?:   int    @go(OwnerPID)
	owner_start?: string @go(OwnerStart)
}

// #PreemptLedger is the on-disk lease set (~/.local/share/charly/preemption/leases.yml).
#PreemptLedger: {
	leases!: [...#PreemptLease] @go(Leases)
}

// #HolderDescriptor is one candidate preemptible holder, pre-projected (PreemptionHolds() +
// HolderAddrFor() + PreemptEffectiveRestore(), sdk/deploykit's FilterPreemptibleHolders) so the
// plugin's holdersToStop is pure config-free coordination. Filled plugin-side now (K1-unblock
// wave 1, off the #ResolvedProject envelope) rather than by a bespoke host reply type.
#HolderDescriptor: {
	name!: string @go(Name)
	holds!: [...string] @go(Holds)
	addr!:    #HolderAddr @go(Addr)
	restore!: string @go(Restore)
}

// #ArbiterInvokeInput is the action-multiplexed input the in-core proxy ships to verb:arbiter.
// Each action populates only the field(s) it needs (the OTHERS stay Go zero-value); claim_addr is
// REQUIRED rather than optional because the original hand-written type's `omitempty` on a
// value-typed (non-pointer) HolderAddr field was already a no-op in encoding/json — Go's
// omitempty never elides a struct value — so marking it required here changes nothing about the
// actual wire bytes, only how honestly the schema describes them.
#ArbiterInvokeInput: {
	action!: string @go(Action)
	claimant?: string @go(Claimant)
	tokens?: [...string] @go(Tokens)
	claim_addr!: #HolderAddr @go(ClaimAddr)
	transient?: bool @go(Transient)
	success?:   bool @go(Success)  // release-claimant
	token?:     string @go(Token) // clear-poison / resource-poisoned
	// GPU-implied-consumer projection (acquire-shared only, K-wave W3a A2): the claimant node's
	// GPU-relevant traits, so verb:arbiter can union its OWN implied-consumer token onto tokens
	// before applying its early-return-on-empty acquire policy — arbiter policy belongs in the
	// arbiter, not the in-core proxy (the former charly/gpu_imply.go, now
	// candy/plugin-preempt/gpu_imply.go). is_group/is_pod_member are pre-derived core-side (the
	// in-core nodeTraits/isPodMember trait resolution — not duplicated plugin-side);
	// security_devices is the claimant's raw security.devices list.
	is_group?:         bool @go(IsGroup)
	is_pod_member?:    bool @go(IsPodMember)
	security_devices?: [...string] @go(SecurityDevices)
}

// #ArbiterInvokeReply is the action-multiplexed reply from verb:arbiter.
#ArbiterInvokeReply: {
	active?: bool @go(Active) // acquire-* : the lease is active (env must be marked)
	"bool"?: bool @go(Bool)   // resource-poisoned
	ledger?: #PreemptLedger @go(Ledger, optional=nillable) // status
	stranded?: [...string] @go(Stranded) // status
	error?: string @go(Error)
}
