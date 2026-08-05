package deploypod

import (
	"testing"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/spec/spec"
)

// candy_by_name_test.go — migrated from charly/build_target_oci_test.go's
// TestGeneratorCandyByNameRemoteQualifiedKey (K-wave 2 cone R1). The host's `Generator.candyByName`
// had been production-dead since the overlay render moved here — every live call already reached
// overlay.go's twin — but its ONLY regression test still lived over there, so deleting the dead core
// method would have silently dropped the guard for an RCA'd build failure. The test moves with the
// code, exercising the surviving implementation.

// TestCandyByNameRemoteQualifiedKey guards the add_candy-on-pod overlay build: a REMOTE add_candy
// candy (fetched via spec.ResolveOpts.ExtraCandyRefs) is keyed in the Candies map under its
// fully-qualified ref, while the compiled plan step's CandyName is the candy's BARE intrinsic name.
// candyByName (the step-emit Op/Builder path's candy resolver) must resolve the bare name to the
// qualified-key candy, or the OpStep build-emit fails with `task emit: candy "<name>" not found`.
// Regression for the K1-alpha add_candy-on-pod-overlay "candy not found" build failure.
func TestCandyByNameRemoteQualifiedKey(t *testing.T) {
	readers := loaderkit.FinalizeScannedCandies(map[string]spec.ScannedCandy{
		"marker": {
			Model: spec.CandyModel{Name: "marker"},
			View:  spec.CandyView{Name: "marker", Remote: true, RepoPath: "github.com/org/repo", SubPathPrefix: "candy/"},
		},
		"local-layer": {
			Model: spec.CandyModel{Name: "local-layer"},
			View:  spec.CandyView{Name: "local-layer"},
		},
	}, nil)

	// Re-key exactly as the live overlay build does: CandyMapKey gives a REMOTE candy its
	// fully-qualified ref and a LOCAL candy its bare name — the very divergence this lookup exists
	// to bridge.
	candies := map[string]deploykit.CandyModel{}
	for _, c := range readers {
		candies[spec.CandyMapKey(c)] = c
	}
	if _, bareKeyed := candies["marker"]; bareKeyed {
		t.Fatal("setup is vacuous: the remote candy is bare-keyed, so the fallback under test is never exercised")
	}

	// Exact (local) key — bare == .Name — still resolves directly.
	if c := candyByName(candies, "local-layer"); c == nil || c.GetName() != "local-layer" {
		t.Fatalf("local-layer: got %v, want .Name=local-layer", c)
	}
	// Bare name resolves the qualified-key remote candy (the regression this guards).
	if c := candyByName(candies, "marker"); c == nil || c.GetName() != "marker" {
		t.Fatalf("marker bare-name lookup returned %v; qualified-key .Name fallback is broken", c)
	}
	// An unknown name is still nil (no accidental match).
	if c := candyByName(candies, "nonexistent"); c != nil {
		t.Fatalf("nonexistent: want nil, got %v", c)
	}
}
