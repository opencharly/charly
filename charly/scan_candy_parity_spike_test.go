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

			newModel, newView, err := loaderkit.ScanCandy(dir, "spike", UnifiedFileName, parseCandyYAML)
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

			diffFields(t, "CandyModel", dir, oldModel, newModel)
			diffFields(t, "CandyView", dir, oldView, newView)
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

	newModel, newView, err := loaderkit.ScanCandy(dir, "spike", UnifiedFileName, parseCandyYAML)
	if err != nil {
		t.Fatalf("new loaderkit.ScanCandy: %v", err)
	}
	newModel.RunOps = old.runOps()
	newView.HasInit = old.HasAnyInit()
	newModel.HasInstallFiles = newModel.HasInstallFiles || len(newModel.RunOps) > 0
	newModel.HasContent = newModel.HasContent || newModel.HasInstallFiles || newView.HasInit

	diffFields(t, "CandyModel", dir, oldModel, newModel)
	diffFields(t, "CandyView", dir, oldView, newView)

	// Assert the actual normalized form landed, not just that old==new (a shared bug would pass
	// the diff above but still be wrong).
	if len(newModel.Port) != 2 || newModel.Port[0].Protocol != "udp" || newModel.Port[0].Port != 5353 {
		t.Errorf("expected udp:5353 normalized PortSpec, got %+v", newModel.Port)
	}
}

// diffFields reports ONLY the top-level struct fields that differ between old and new, by name
// and value — a whole-struct %+v dump is too noisy for these deeply-nested candy views.
func diffFields(t *testing.T, label, dir string, oldV, newV any) {
	t.Helper()
	ov := reflect.ValueOf(oldV)
	nv := reflect.ValueOf(newV)
	ot := ov.Type()
	for i := 0; i < ot.NumField(); i++ {
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

	_, _, newErr := loaderkit.ScanCandy(dir, "spike", UnifiedFileName, parseCandyYAML)
	if newErr == nil {
		t.Fatal("expected new loaderkit.ScanCandy to fail on malformed YAML")
	}

	if oldErr.Error() != newErr.Error() {
		t.Errorf("error text mismatch:\nOLD: %v\nNEW: %v", oldErr, newErr)
	}
}
