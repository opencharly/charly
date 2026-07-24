package main

import (
	"testing"

	"github.com/opencharly/sdk/spec"
)

// arbiter_dispatch_test.go — the C9 externalized-arbiter DISPATCH integration test: it drives the
// EXACT path the check-runner uses at bed bring-up (acquireResourceForClaimant → the in-core proxy
// → the compiled-in candy/plugin-preempt verb:arbiter → HostBuild("resolved-project") for its
// gather/resources reads (K1-unblock wave 1) → the lease ledger), then proves the lease SURFACES
// via the arbiter status dispatch (the generic core→verb registry bridge; the externalized `charly
// preempt status` reaches it via InvokeProvider instead). This is what the seam-faked unit suite
// (which tests the arbiter LOGIC in-plugin) cannot cover: the compiled-in dispatch + reverse-channel
// round-trip + real persistence. It is the resource-free (ZERO GPU) analogue of the
// check-preempt-arbiter-pod bed, hermetic (temp HOME for the ledger, temp cwd so no project
// holders/resources are gathered).
//
// This unit test drives the DIRECT-claimant acquire shim (requires_exclusive on the node the shim
// sees) in isolation — the arbiter DISPATCH + reverse-channel + persistence path, seam-free. The
// group-MEMBER live-preemption path (a preemptible holder member actually stopped by a
// requires_exclusive claimant member) is proven live by the check-preempt-live-pod bed: that path
// now works because persistBedDeployOverrides seeds a member's arbiter fields into the per-host
// config, so a member's `charly start` reloads requires_exclusive/preemptible and the arbiter
// fires (the earlier group draft dropped those fields → empty ledger; the persistence gap is
// fixed). THIS test remains the seam-free dispatch witness the live bed cannot isolate.
func TestArbiterExternalizedDispatch_AcquirePersistsAndSurfaces(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // hermetic lease ledger (~/.local/share/charly/preemption/…)
	t.Chdir(t.TempDir())          // no charly.yml → gather/resources host seams see no holders/resources
	t.Setenv(envPreemptLeaseHeld, "")

	claimant := "check-preempt-arbiter-pod"
	node := spec.BundleNode{Target: "pod", RequiresExclusive: []string{"test-lock"}}

	// The runner's bed-arbiter path: acquire an exclusive claim for the bed. A SELECTOR-LESS
	// token (no resource: gpu def) → applyMode SKIPS the device flip (ZERO GPU) but the lease is
	// STILL persisted through the compiled-in verb:arbiter over the InvokeProvider dispatch.
	lease, err := acquireExclusiveForClaimant(claimant, node, true)
	if err != nil {
		t.Fatalf("acquireExclusiveForClaimant through externalized verb:arbiter: %v", err)
	}
	if lease == nil || !lease.active {
		t.Fatalf("expected an ACTIVE lease from the externalized acquire, got %+v", lease)
	}

	// The lease must SURFACE via the arbiter status dispatch (the generic core→verb registry bridge
	// the core lease lifecycle uses; the externalized `charly preempt status` reaches it via InvokeProvider).
	sr, serr := arbiterInvoke(spec.ArbiterInvokeInput{Action: spec.ArbiterActionStatus})
	if serr != nil {
		t.Fatalf("arbiter status through externalized verb:arbiter: %v", serr)
	}
	ledger := sr.Ledger
	if ledger == nil || len(ledger.Leases) != 1 {
		t.Fatalf("expected exactly one persisted lease, got %+v", ledger)
	}
	lz := ledger.Leases[0]
	if lz.Claimant != claimant || len(lz.Tokens) != 1 || lz.Tokens[0] != "test-lock" {
		t.Fatalf("lease did not surface the claimant + token: %+v", lz)
	}

	// Release restores (no holders → no-op) and clears the lease.
	if rerr := newResourceArbiter().ReleaseClaimant(claimant, true); rerr != nil {
		t.Fatalf("proxy ReleaseClaimant: %v", rerr)
	}
	sr, _ = arbiterInvoke(spec.ArbiterInvokeInput{Action: spec.ArbiterActionStatus})
	ledger = sr.Ledger
	if ledger != nil && len(ledger.Leases) != 0 {
		t.Fatalf("lease should be gone after release, got %+v", ledger.Leases)
	}
}
