package fleet

// Relocated from charly/check_bed_run_test.go (#55 decoupling cone, Batch C,
// per the binding file-ownership ruling): TestBedCheckLiveRefs asserts
// fleet.BedCheckLiveRefs directly — a genuine deploykit-behavior
// assertion (per Ambiguous-item-1's ruling), not charly-loader integration
// coverage, so it moves here rather than staying in charly/check_bed_run_test.go
// alongside the bed-persist cluster's genuinely-integration tests.

import (
	"testing"

	"github.com/opencharly/spec/fleet"
	"github.com/opencharly/spec/spec"
)

// stubTraitsFor is a test-local stand-in for charly's deployTraitsFor (which
// resolves a substrate word's declared #DeployTraits from the live provider
// registry): "android" is the one traits value this test cares about beyond
// the zero-value default (Venue "parent" — no own venue, so BedCheckLiveRefs
// skips it).
func stubTraitsFor(word string) *spec.DeployTraits {
	if word == "android" {
		return &spec.DeployTraits{Venue: "parent"}
	}
	return &spec.DeployTraits{}
}

// TestBedCheckLiveRefs proves `charly check run <bed>` check-lives the substrate AND
// every nested child (sorted, dotted) — so a nested pod's baked candy/box
// check runs against its real venue. Before the nested-check fix this produced
// only [name], so a nested selkies-kde pod was deployed but never evaluated.
func TestBedCheckLiveRefs(t *testing.T) {
	// Flat bed: just the substrate (identical to the prior behavior).
	if got := fleet.BedCheckLiveRefs("check-pod", nil); len(got) != 1 || got[0] != "check-pod" {
		t.Fatalf("flat bed: got %v, want [check-pod]", got)
	}
	// Nested bed: substrate first, then each child as a sorted dotted path.
	nested := map[string]*spec.FleetNode{
		"selkies-kde": {Target: "pod"},
		"cuda-pod":    {Target: "pod"},
	}
	got := fleet.BedCheckLiveRefs("check-cachyos-gpu-vm", nested)
	want := []string{
		"check-cachyos-gpu-vm",
		"check-cachyos-gpu-vm.cuda-pod", // sorted before selkies-kde
		"check-cachyos-gpu-vm.selkies-kde",
	}
	if len(got) != len(want) {
		t.Fatalf("nested bed: got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("nested bed ref[%d]: got %q, want %q", i, got[i], want[i])
		}
	}

	// Android child: a target:android nested child shares the parent pod's
	// venue (its app-presence checks are baked into the parent's
	// android-emulator-layer and run in the parent ref) and has NO own venue
	// `charly check live` can resolve — so it gets NO dotted hop, while a pod sibling
	// still does. This is the check-coverage gate for the e740430 defect: a hop
	// for an android child wrongly resolved to a non-existent
	// `charly-<parent>.device` container, failing every nested pod→android bed's R10.
	androidNested := map[string]*spec.FleetNode{
		"web":    {Target: "pod"},
		"device": {Target: "android"},
	}
	// Stamp the descent traits (P9) exactly as the loader does — production passes
	// BedCheckLiveRefs children from the stamped tree; the android skip reads the venue trait.
	for _, c := range androidNested {
		spec.StampDescent(c, stubTraitsFor)
	}
	gotA := fleet.BedCheckLiveRefs("check-android-emulator-pod", androidNested)
	wantA := []string{
		"check-android-emulator-pod",
		"check-android-emulator-pod.web", // pod child kept; android "device" omitted
	}
	if len(gotA) != len(wantA) {
		t.Fatalf("android bed: got %v, want %v (android child must be omitted)", gotA, wantA)
	}
	for i := range wantA {
		if gotA[i] != wantA[i] {
			t.Errorf("android bed ref[%d]: got %q, want %q", i, gotA[i], wantA[i])
		}
	}
}
