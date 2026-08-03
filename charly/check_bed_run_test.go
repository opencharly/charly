package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/opencharly/spec/spec"

	"github.com/opencharly/sdk/deploykit"
)

// TestValidateCheckBeds_TargetEnum / _VmRefMustResolve / _LocalRefMustResolve
// relocated to candy/plugin-loader/check_bed_run_test.go (#55 decoupling cone,
// Batch C) — they asserted loaderkit.ValidateCheckBeds directly, zero charly
// coupling.

// TestBedCheckLiveRefs relocated to candy/plugin-bundle/bed_check_live_refs_test.go
// (#55 decoupling cone, Batch C, per the binding file-ownership ruling on
// Ambiguous item 1): it asserted spec.BedCheckLiveRefs directly — a genuine
// deploykit-behavior assertion, not charly-loader integration coverage.

// TestCheckBeds_DerivesFromDisposableBundles asserts the R10 bed set is derived
// from the `disposable: true` bundles in the Deploy map (the separate kind:check
// block was removed — a bed IS a disposable bundle); a non-disposable deploy is
// NOT a bed.
func TestCheckBeds_DerivesFromDisposableBundles(t *testing.T) {
	uf := &spec.UnifiedFile{
		Bundle: map[string]spec.BundleNode{
			"sample-pod-bed":   {Target: "pod", Image: "sample-image", Disposable: new(true)},
			"sample-vm-bed":    {Target: "vm", From: "sample-vm", Disposable: new(true)},
			"sample-local-bed": {Target: "local", From: "sample-local", Disposable: new(true)},
			"plain-deploy":     {Target: "pod", Image: "prod"}, // not disposable → not a bed
		},
	}
	beds := uf.CheckBeds()
	if got := len(beds); got != 3 {
		t.Errorf("CheckBeds() = %d entries, want 3 (only disposable bundles)", got)
	}
	if _, ok := beds["plain-deploy"]; ok {
		t.Error("a non-disposable deploy must NOT be enumerated as a bed")
	}
}

// TestPersistBedDeployOverrides_SeedsPortBeforeConfig pins the fix for the
// bug class where a kind:check pod bed's project-declared deploy-shaped fields
// (port:/volume:/env:/tunnel:) never reached the per-host deploy.yml: charly check
// run shelled out `charly bundle add`/`charly config` with just the bed NAME, and both
// source port/security/network from the IMAGE LABELS (gating port writes behind
// an operator -p), so the bed's `port: 45434:11434` remap silently fell back to
// the image default and collided with a same-image production deploy at start.
// deploykit.PersistBedDeployOverrides seeds the bed node's overrides up front so the
// existing charly config -> MergeDeployOntoMetadata -> quadlet path honors them. The former
// host-side persistBedDeployOverrides wrapper moved PLUGIN-SIDE to candy/plugin-check
// (#55 coneC-dsh β1); this test calls deploykit.PersistBedDeployOverrides directly with a test-local
// marshalNode (deploykit.MarshalBundleNode, nil primaries — the bed has no plugin-verb sugar) + a
// nil reader (the DeployStateHost-backed fallback), verifying the SAME seed behavior the plugin-side
// bed persist relies on.
//
// This STAYS in charly/ per Ambiguous-item-1's ruling: testLoadBundleConfig
// calls charly's own LoadUnified() to round-trip a REAL project load through
// deploykit's persistence layer — a genuine charly-loader + deploykit
// INTEGRATION test, not a pure deploykit capability test, so it cannot be
// cleanly split onto a plugin's own fixtures.
func TestPersistBedDeployOverrides_SeedsPortBeforeConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "charly"), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// A pre-existing unrelated deploy must survive the seed (merge, not clobber).
	// Compact node-form: the port collection lives INLINE in the pod value.
	initialYAML := `version: 2026.204.1223
ollama:
    pod:
        image: ollama
        port:
            - 11434:11434
`
	path := filepath.Join(dir, "charly", "charly.yml")
	if err := os.WriteFile(path, []byte(initialYAML), 0600); err != nil {
		t.Fatalf("write initial: %v", err)
	}

	// A bed whose key differs from its image and whose port remaps off the
	// image default — exactly the check-cachyos-ollama-pod shape.
	bed := spec.BundleNode{
		Target:     "pod",
		Image:      "ollama",
		Port:       []string{"45434:11434"},
		Disposable: new(true),
		Lifecycle:  "dev",
	}
	marshalNode := testBedMarshalNode
	deploykit.PersistBedDeployOverrides("check-cachyos-ollama-pod", bed, bedExternalInPlace(bed.Target), marshalNode, testLoadBundleConfig)

	dc, err := testLoadBundleConfig()
	if err != nil {
		t.Fatalf("reload after seed: %v", err)
	}
	entry, ok := dc.Bundle["check-cachyos-ollama-pod"]
	if !ok {
		t.Fatal("bed entry not seeded into deploy.yml")
	}
	if len(entry.Port) != 1 || entry.Port[0] != "45434:11434" {
		t.Errorf("bed port not seeded: got %v, want [45434:11434]", entry.Port)
	}
	if entry.Image != "ollama" || entry.Target != "pod" {
		t.Errorf("bed image/target not seeded: got image=%q target=%q", entry.Image, entry.Target)
	}
	if entry.Disposable == nil || !*entry.Disposable {
		t.Error("bed disposable not seeded (the check-runner requires it to authorize the unattended fresh-rebuild)")
	}
	// The sibling production deploy must be untouched (distinct key).
	sib, ok := dc.Bundle["ollama"]
	if !ok || len(sib.Port) != 1 || sib.Port[0] != "11434:11434" {
		t.Errorf("sibling 'ollama' deploy clobbered: got %+v", sib)
	}
}
