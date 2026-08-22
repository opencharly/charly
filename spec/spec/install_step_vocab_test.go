package spec

import "testing"

// TestLocalPkgInstallStepIR exercises the IR contract for LocalPkgInstallStep
// (relocated from charly/localpkg_test.go, K3 cone2 test closure): kind, scope
// (system), venue (host-native), gate (none), reverse (no ledger ops — like
// apk) — a pure spec.LocalPkgInstallStep test with zero charly-core
// dependency, and no prior duplicate found anywhere in the tree.
func TestLocalPkgInstallStepIR(t *testing.T) {
	s := &LocalPkgInstallStep{PackageName: "charly", Version: "2026.225.1200", CandyName: "charly"}
	if s.Kind() != StepKindLocalPkgInstall {
		t.Errorf("Kind() = %q, want %q", s.Kind(), StepKindLocalPkgInstall)
	}
	if s.Scope() != ScopeSystem {
		t.Errorf("Scope() = %v, want ScopeSystem", s.Scope())
	}
	if s.Venue() != VenueHostNative {
		t.Errorf("Venue() = %v, want VenueHostNative", s.Venue())
	}
	if s.RequiresGate() != GateNone {
		t.Errorf("RequiresGate() = %v, want GateNone", s.RequiresGate())
	}
	if s.Reverse() != nil {
		t.Errorf("Reverse() = %v, want nil (OS package is the substrate's own, not ledger-reversed)", s.Reverse())
	}
}
