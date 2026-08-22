package phase

import "testing"

func TestNormalizePhaseKnown(t *testing.T) {
	for _, p := range PhaseOrder {
		if got := NormalizePhase(p); got != p {
			t.Fatalf("NormalizePhase(%q) = %q, want %q", p, got, p)
		}
	}
}

func TestNormalizePhaseUnknownDefaultsRuntime(t *testing.T) {
	for _, p := range []string{"", "unknown", "midnight", "BOOTSTRAP"} {
		if got := NormalizePhase(p); got != PhaseRuntime {
			t.Fatalf("NormalizePhase(%q) = %q, want %q", p, got, PhaseRuntime)
		}
	}
}

func TestPhaseOrderAscending(t *testing.T) {
	want := []string{PhasePreflight, PhaseBootstrap, PhaseSchema, PhaseLoad, PhaseBuild, PhaseRuntime}
	if len(PhaseOrder) != len(want) {
		t.Fatalf("PhaseOrder len = %d, want %d", len(PhaseOrder), len(want))
	}
	for i, p := range PhaseOrder {
		if p != want[i] {
			t.Fatalf("PhaseOrder[%d] = %q, want %q", i, p, want[i])
		}
	}
}

func TestPhaseConstantsDistinct(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range PhaseOrder {
		if seen[p] {
			t.Fatalf("phase %q duplicated in PhaseOrder", p)
		}
		seen[p] = true
	}
}
