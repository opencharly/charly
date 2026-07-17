package main

// scan_candy_parity_spike_test.go — the W9 RDD SPIKE proving loaderkit.ScanCandy produces
// byte-identical (CandyModel, CandyView) output to the pre-move scanCandy+populateCandyFromYAML+
// projectCandyModel/projectCandyView pipeline, on real candies exercising the three derived-logic
// branches (bake_plugin→require, package-section derivation, port normalization) plus a negative
// (malformed-manifest) case. THROWAWAY per RDD — deleted once the port lands; the knowledge (byte
// parity confirmed) is what carries into the plan, not this file.

import (
	"os"
	"reflect"
	"testing"

	"github.com/opencharly/sdk/loaderkit"
)

func TestScanCandyParitySpike(t *testing.T) {
	cases := []string{
		"../candy/charly-mcp",             // bake_plugin -> require implication
		"../candy/charly",                 // distro:/package: derivation (tag + format sections)
		"../candy/android-emulator-layer", // port normalization (plain-int form)
	}
	for _, dir := range cases {
		t.Run(dir, func(t *testing.T) {
			old, err := scanCandy(dir, "spike", UnifiedFileName)
			if err != nil {
				t.Fatalf("old scanCandy: %v", err)
			}
			oldModel := projectCandyModel(old)
			oldView := projectCandyView(old)

			newModel, newView, newRefs, err := loaderkit.ScanCandy(dir, "spike", UnifiedFileName, parseCandyYAML)
			if err != nil {
				t.Fatalf("new loaderkit.ScanCandy: %v", err)
			}

			// The host-side second pass ScanCandy's doc comment describes: RunOps (registry-
			// adjacent opInContext/VerbCatalog, task #39, still core) and HasInit
			// (PopulateCandyInitSystem's cross-candy InitConfig resolution) are NOT scan-computable
			// — simulate that pass here exactly as the real ScanAllCandy wrapper will, then re-OR
			// the two predicates the same way ScanCandy's own partial computation expects.
			newModel.RunOps = old.runOps()
			newView.HasInit = old.HasAnyInit()
			newModel.HasInstallFiles = newModel.HasInstallFiles || len(newModel.RunOps) > 0
			newModel.HasContent = newModel.HasContent || newModel.HasInstallFiles || newView.HasInit

			// Simulate the host's finalize step. QualifyRemoteSiblingDeps is DELIBERATELY not
			// called here: these are all LOCALLY-scanned candy dirs, and the pre-move
			// qualifyRemoteSiblingDeps is only ever invoked from the ScanRemoteCandy path (a
			// freshly-fetched remote candy) — calling it here would qualify every ref against an
			// empty RepoPath/SubPathPrefix and corrupt .Resolved, which is not what the real
			// pipeline does for a local candy. FinalizeCandyRefs alone (no qualification) proves
			// Finding 4's fix (CandyRefs carrying the rich form through to a LATER finalize,
			// instead of bare-stringing at scan time) reproduces the old byte-exact LOCAL result.
			loaderkit.FinalizeCandyRefs(&newModel, &newView, newRefs)

			// BakePlugin is skipped in the CandyModel diff below: the pre-move projectCandyModel
			// NEVER populated it (Finding 3 — a genuine schema gap, not a parity target), so OLD is
			// always empty here regardless of the candy's own bake_plugin: declaration. Assert the
			// NEW value directly instead — it must equal the bare-string projection of the rich
			// BakePlugin refs FinalizeCandyRefs just wrote.
			if want := bareRefs(old.BakePlugin); !reflect.DeepEqual(want, newModel.BakePlugin) {
				t.Errorf("CandyModel.BakePlugin: want bare projection %+v, got %+v", want, newModel.BakePlugin)
			}
			diffFields(t, "CandyModel", dir, oldModel, newModel, "BakePlugin")
			diffFields(t, "CandyView", dir, oldView, newView)

			// Parity on the RICH pre-qualification form itself (the carrier Finding 4 introduced) —
			// against the live *Candy's own Require/IncludedCandy/BakePlugin fields, which pre-move
			// hold the SAME rich CandyRefEntry shape (deploykit.CandyRef is a passthrough alias).
			if !reflect.DeepEqual(old.Require, newRefs.Require) {
				t.Errorf("CandyRefs.Require mismatch for %s:\nOLD: %+v\nNEW: %+v", dir, old.Require, newRefs.Require)
			}
			if !reflect.DeepEqual(old.IncludedCandy, newRefs.IncludedCandy) {
				t.Errorf("CandyRefs.IncludedCandy mismatch for %s:\nOLD: %+v\nNEW: %+v", dir, old.IncludedCandy, newRefs.IncludedCandy)
			}
			if !reflect.DeepEqual(old.BakePlugin, newRefs.BakePlugin) {
				t.Errorf("CandyRefs.BakePlugin mismatch for %s:\nOLD: %+v\nNEW: %+v", dir, old.BakePlugin, newRefs.BakePlugin)
			}
		})
	}
}

// TestScanCandyParitySpikePortProtocol is the synthetic fixture for the "udp:port" protocol-
// suffixed port form — no real candy in this repo currently authors one, so this constructs the
// minimal manifest that exercises populateCandyFromYAML's protocol-suffix normalization branch
// (fmt.Sprintf("%d/udp", p.Port) vs the bare "%d" form).
func TestScanCandyParitySpikePortProtocol(t *testing.T) {
	dir := t.TempDir()
	manifest := "candy:\n  version: 2026.001.0000\n  description: spike fixture\n  port:\n    - \"udp:5353\"\n    - 8080\n"
	if err := os.WriteFile(dir+"/"+UnifiedFileName, []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	old, err := scanCandy(dir, "spike", UnifiedFileName)
	if err != nil {
		t.Fatalf("old scanCandy: %v", err)
	}
	oldModel := projectCandyModel(old)
	oldView := projectCandyView(old)

	newModel, newView, newRefs, err := loaderkit.ScanCandy(dir, "spike", UnifiedFileName, parseCandyYAML)
	if err != nil {
		t.Fatalf("new loaderkit.ScanCandy: %v", err)
	}
	newModel.RunOps = old.runOps()
	newView.HasInit = old.HasAnyInit()
	newModel.HasInstallFiles = newModel.HasInstallFiles || len(newModel.RunOps) > 0
	newModel.HasContent = newModel.HasContent || newModel.HasInstallFiles || newView.HasInit
	loaderkit.FinalizeCandyRefs(&newModel, &newView, newRefs)

	diffFields(t, "CandyModel", dir, oldModel, newModel)
	diffFields(t, "CandyView", dir, oldView, newView)

	// Assert the actual normalized form landed, not just that old==new (a shared bug would pass
	// the diff above but still be wrong).
	if len(newModel.Port) != 2 || newModel.Port[0].Protocol != "udp" || newModel.Port[0].Port != 5353 {
		t.Errorf("expected udp:5353 normalized PortSpec, got %+v", newModel.Port)
	}
}

// diffFields reports ONLY the top-level struct fields that differ between old and new, by name
// and value — a whole-struct %+v dump is too noisy for these deeply-nested candy views. skip
// names fields the caller has already asserted separately (a deliberate old/new divergence, e.g.
// a genuine pre-move schema gap being fixed by this move — never a silent parity waiver).
func diffFields(t *testing.T, label, dir string, oldV, newV any, skip ...string) {
	t.Helper()
	skipSet := make(map[string]bool, len(skip))
	for _, s := range skip {
		skipSet[s] = true
	}
	ov := reflect.ValueOf(oldV)
	nv := reflect.ValueOf(newV)
	ot := ov.Type()
	for i := 0; i < ot.NumField(); i++ {
		if skipSet[ot.Field(i).Name] {
			continue
		}
		of := ov.Field(i).Interface()
		nf := nv.Field(i).Interface()
		if !reflect.DeepEqual(of, nf) {
			t.Errorf("%s.%s mismatch for %s:\nOLD: %+v\nNEW: %+v", label, ot.Field(i).Name, dir, of, nf)
		}
	}
}

// TestScanCandyParitySpikeMalformed is the required negative case: a candy directory whose
// manifest fails to parse must fail IDENTICALLY (same error text) through both paths.
func TestScanCandyParitySpikeMalformed(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/"+UnifiedFileName, []byte("candy:\n  version: [this is not valid\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, oldErr := scanCandy(dir, "spike", UnifiedFileName)
	if oldErr == nil {
		t.Fatal("expected old scanCandy to fail on malformed YAML")
	}

	_, _, _, newErr := loaderkit.ScanCandy(dir, "spike", UnifiedFileName, parseCandyYAML)
	if newErr == nil {
		t.Fatal("expected new loaderkit.ScanCandy to fail on malformed YAML")
	}

	if oldErr.Error() != newErr.Error() {
		t.Errorf("error text mismatch:\nOLD: %v\nNEW: %v", oldErr, newErr)
	}
}
