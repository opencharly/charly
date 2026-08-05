package main

import (
	"testing"

	"github.com/opencharly/spec/spec"
)

// arbiter_dispatch_test.go — the arbiter RELEASE-chain dispatch test. It drives the ONE
// surviving core→verb:arbiter path (the op="remove" release bracket, folded from the deleted
// preempt.go into host_build_pod_lifecycle_dispatch.go): releaseResourceClaim →
// newResourceArbiter().ReleaseClaimant → arbiterInvoke → the compiled-in candy/plugin-preempt
// verb:arbiter → the lease ledger. It proves the compiled-in dispatch + in-proc reverse-channel
// round-trip + persistence surface still work from core after preempt.go's dissolution. The
// former acquire-side shims (acquireExclusiveForClaimant & co.) are GONE — their production
// callers went peer-dispatch (candy/plugin-check's bed_session, candy/plugin-vm's
// vm_arbiter_shim, candy/plugin-bundle's handleLifecycleSimple all Invoke verb:arbiter
// directly), so no core test drives them anymore; the live preemption path is proven by the
// check-preempt-live-pod bed.
//
// This unit test is the resource-free (ZERO GPU) analogue of the check-preempt-arbiter-pod bed,
// hermetic (temp HOME for the ledger, temp cwd so no project holders/resources are gathered).
func TestArbiterReleaseDispatch_SurvivesAndSurfaces(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // hermetic lease ledger (~/.local/share/charly/preemption/…)
	t.Chdir(t.TempDir())          // no charly.yml → gather/resources host seams see no holders/resources
	t.Setenv(envPreemptLeaseHeld, "")

	claimant := "check-preempt-arbiter-pod"

	// The release chain dispatches to the compiled-in verb:arbiter without error. With no lease
	// held, restore is a no-op (nothing stopped) — the persistence surface must read empty after.
	if rerr := newResourceArbiter().ReleaseClaimant(claimant, true); rerr != nil {
		t.Fatalf("proxy ReleaseClaimant: %v", rerr)
	}
	sr, serr := arbiterInvoke(spec.ArbiterInvokeInput{Action: spec.ArbiterActionStatus})
	if serr != nil {
		t.Fatalf("arbiter status through the release chain: %v", serr)
	}
	if sr.Ledger != nil && len(sr.Ledger.Leases) != 0 {
		t.Fatalf("lease ledger should be empty after a no-op release, got %+v", sr.Ledger.Leases)
	}

	// releaseResourceClaim itself (the op="remove" bracket body) is best-effort: no error
	// surfaces, and with an outer orchestrator guard set it skips entirely.
	releaseResourceClaim(claimant)
	t.Setenv(envPreemptLeaseHeld, "outer")
	releaseResourceClaim(claimant)
}
