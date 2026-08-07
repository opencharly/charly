package main

import (
	"testing"

	"github.com/opencharly/spec/spec"
)

// TestValidateCheckBeds_TargetEnum / _VmRefMustResolve / _LocalRefMustResolve
// relocated to candy/plugin-loader/check_bed_run_test.go (#55 decoupling cone,
// Batch C) — they asserted loaderkit.ValidateCheckBeds directly, zero charly
// coupling.

// TestBedCheckLiveRefs relocated to candy/plugin-fleet/bed_check_live_refs_test.go
// (#55 decoupling cone, Batch C, per the binding file-ownership ruling on
// Ambiguous item 1): it asserted spec.BedCheckLiveRefs directly — a genuine
// deploykit-behavior assertion, not charly-loader integration coverage.

// TestPersistBedDeployOverrides_SeedsPortBeforeConfig relocated to
// candy/plugin-fleet/bed_persist_writer_test.go (#55 final-tail split-by-assertion round,
// team-lead directive 2026-08-03): it pins deploykit.PersistBedDeployOverrides' OWN
// merge-not-clobber contract — genuine deploykit-mechanism behavior, not charly-loader-specific
// parsing (the prior "stays in charly per Ambiguous-item-1" ruling predated the gate that forced
// this split question).

// TestCheckBeds_DerivesFromDisposableFleets asserts the R10 bed set is derived
// from the `disposable: true` fleets in the Deploy map (the separate kind:check
// block was removed — a bed IS a disposable fleet); a non-disposable deploy is
// NOT a bed.
func TestCheckBeds_DerivesFromDisposableFleets(t *testing.T) {
	uf := &spec.UnifiedFile{
		Fleet: map[string]spec.FleetNode{
			"sample-pod-bed":   {Target: "pod", Image: "sample-image", Disposable: new(true)},
			"sample-vm-bed":    {Target: "vm", From: "sample-vm", Disposable: new(true)},
			"sample-local-bed": {Target: "local", From: "sample-local", Disposable: new(true)},
			"plain-deploy":     {Target: "pod", Image: "prod"}, // not disposable → not a bed
		},
	}
	beds := uf.CheckBeds()
	if got := len(beds); got != 3 {
		t.Errorf("CheckBeds() = %d entries, want 3 (only disposable fleets)", got)
	}
	if _, ok := beds["plain-deploy"]; ok {
		t.Error("a non-disposable deploy must NOT be enumerated as a bed")
	}
}
