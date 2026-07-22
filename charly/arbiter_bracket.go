package main

import "github.com/opencharly/sdk/spec"

// acquireForBracket / releaseForBracket are swap points to the real preempt.go shims, isolated so
// tests can assert the bracket's CALL ORDER (acquire-before-dispatch, release-on-failure,
// release-after-stop) without a live verb:arbiter plugin connection.
var (
	acquireForBracket = acquireResourceForClaimant
	releaseForBracket = func(claimant string) { releaseResourceClaim(claimant) }
)

// arbiter_bracket.go — the core-resident arbiter acquire/release BRACKET (S3b, Unit-6 design Q1).
//
// The former core-resident substrate lifecycle proxy's Start/Stop used to bracket the shared
// resource-arbiter claim IN-LINE, around the substrate dispatch itself (acquire BEFORE OpStart, release ON THE FAILURE PATH,
// release AFTER OpStop) — see the historical note this file's functions preserve verbatim below.
// S3b moves the substrate DISPATCH (the actual OpStart/OpStop wire call) to candy/plugin-bundle;
// this file keeps the bracket call sites for now (see MIGRATION INVENTORY below for the tracked
// exit). The mechanism: the CHARLY_PREEMPT_LEASE mutex (preempt.go) is process-ENV state —
// acquireResourceForClaimant sets it via os.Setenv, releaseResourceClaim reads it via os.Getenv,
// and the whole point is that a NESTED `charly` subprocess (spawned by the SAME outer invocation
// after the env is set) inherits it and skips re-acquiring. That property holds ONLY when the
// acquiring code runs in the SAME OS process as the host `charly` binary — an out-of-process
// plugin's own os.Setenv would never reach the host's env, so a bare in-plugin acquire/release
// would silently break the nested-subprocess skip for that placement (dual-placement by
// construction — every plugin must behave correctly compiled-in AND out-of-process).
//
// MIGRATION INVENTORY: this file (and the "What did NOT move" bracket call sites in
// candy/plugin-bundle/deploy_target.go) is UNTIL-K4 (deploy-state/arbitration family) — it exits
// via the SAME HostBuild-reverse-leg pattern this PR already applies to
// host_build_deploy_config_save_state.go (Q2): the plugin calls back a HostBuild kind for its own
// acquire/release around its own dispatch, so the actual os.Setenv/os.Getenv still EXECUTE in the
// host process (preserving the nested-subprocess env-inheritance property) while OWNERSHIP of the
// bracket call — deciding when to acquire/release, around which dispatch — moves plugin-side. Not
// a "stays core forever" claim: a HostBuild reverse-leg is available today (the Q2 precedent
// proves it), the restructure is scoped to the K4 wave.
//
// hasPlan gates whether the bracket applies at all — the CALLER (pluginDeployTarget.Start/Stop,
// unified_targets.go) derives it directly from lifecycleStartPlanHooks[word] /
// lifecycleStopPlanHooks[word]'s presence (pod_lifecycle_dispatch.go, unmoved) — the SAME table
// that resolves the plan payload, not a separate mirror (R3: one source of truth for "does this
// substrate need a plan"). Today only "pod" registers one (P13-KERNEL step-4(ii)); "vm" manages
// its OWN arbiter interaction via a nested `charly vm start`/`stop` reentry and must NEVER be
// double-bracketed here (unchanged from pre-move behavior — vm was never gated into this bracket
// either).
//
// arbiterBracketedStart runs dispatch (the substrate's actual Start op, now reaching
// candy/plugin-bundle through the registry) bracketed by the shared resource-arbiter claim:
// acquire BEFORE dispatch, release ON THE FAILURE PATH (a failed dispatch must not leak the
// claim). The caller resolves the Start plan-hook BEFORE calling this function (a deliberate
// reordering vs. the pre-move former core-resident substrate lifecycle proxy's Start, which
// acquired first then released if the plan-hook failed) — since a plan-hook failure means dispatch
// is never even attempted, no claim needs to exist yet either way; the net outcome (no claim
// survives a plan-hook failure) is identical, just reached by never acquiring rather than
// acquiring-then-releasing. node is
// required when hasPlan is true (the claim fields live on it); a nil node with hasPlan true is a
// caller bug, treated as "no claim" rather than a panic.
func arbiterBracketedStart(name string, node *spec.BundleNode, hasPlan bool, dispatch func() error) error {
	if !hasPlan || node == nil {
		return dispatch()
	}
	if _, err := acquireForBracket(name, *node, false); err != nil {
		return err
	}
	if err := dispatch(); err != nil {
		releaseForBracket(name) // release-on-failure: a failed start must not leak the claim
		return err
	}
	return nil
}

// arbiterBracketedStop runs dispatch (the substrate's actual Stop op) then releases the claim
// AFTER, unconditionally (success or failure of the dispatch itself) — matching the former
// core-resident substrate lifecycle proxy's Stop's "release the persistent claim after stop"
// behavior. Deliberate
// simplification vs. the pre-move code: the prior in-line Stop skipped the release when its
// SEPARATE plan-hook call (pre-dispatch) failed, but that distinction required observing the
// plan-hook's own error before the substrate call — a signal that no longer exists once plan
// resolution moves inside the plugin dispatch (S3b Q3). Since a plan-hook marshal essentially
// never fails (spec.PodStopOpts{Unmount: bool} has no fallible construction) and the safe default
// on ANY Stop-path error is to release rather than leak the lease, this simplification is a
// deliberate, documented choice — never a silent behavior drift.
func arbiterBracketedStop(name string, hasPlan bool, dispatch func() error) error {
	err := dispatch()
	if hasPlan {
		releaseForBracket(name) // release the persistent claim after stop
	}
	return err
}
