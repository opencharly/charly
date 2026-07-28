package main

import (
	"os"
	"path/filepath"
	"testing"
)

// render_seam_cache_test.go — the regression for the K3 host-prep move (coneB-render): a first cut
// of newCandyScanGenerator dropped the box-resolve pass entirely, leaving gen.Boxes empty. That
// broke host_build_render_seam.go's RenderSeamInlineBuilder case (renderSeamGenBox's gen.Boxes[boxName]
// lookup always "not found") — invisible to the `charly box generate` byte-parity smoke test, which
// never exercises the inline-builder reverse-channel seam. Caught by tracing every reader of the
// cached Generator (per the team-lead's watch-point), not by the box-generate smoke test.

// TestNewCandyScanGeneratorPopulatesBoxes proves the cheap render-seam-floor constructor resolves
// every requested box (needed for resolveInlineBuilderSeam's img.Tags/img.Name — see
// resolveBuilderStage) with the SAME Name/Tags the full NewGenerator would produce, using a real
// project fixture (box/fedora's "fedora-builder", a builder-based image exercising a real
// distro/tags resolve).
func TestNewCandyScanGeneratorPopulatesBoxes(t *testing.T) {
	repoRoot, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(filepath.Dir(repoRoot), "box", "fedora")
	const boxName = "fedora-builder"

	t.Cleanup(snapshotProviderState())

	cheap, err := newCandyScanGenerator(dir, false, nil)
	if err != nil {
		t.Fatalf("newCandyScanGenerator: %v", err)
	}
	cheapImg := cheap.Boxes[boxName]
	if cheapImg == nil {
		t.Fatalf("newCandyScanGenerator: box %q not found in Boxes — the render-seam floor's "+
			"RenderSeamInlineBuilder case would fail 'box not found' for every request", boxName)
	}

	full, err := NewGenerator(dir, "", boxResolveOpts([]string{boxName}, false))
	if err != nil {
		t.Fatalf("NewGenerator: %v", err)
	}
	fullImg := full.Boxes[boxName]
	if fullImg == nil {
		t.Fatalf("NewGenerator: box %q not found (fixture problem)", boxName)
	}

	if cheapImg.Name != fullImg.Name {
		t.Errorf("Name = %q, want %q (byte-parity with the full resolve)", cheapImg.Name, fullImg.Name)
	}
	if len(cheapImg.Tags) == 0 {
		t.Fatalf("Tags is empty — resolveBuilderStage's spec.BuildEnv{Distros: img.Tags} would carry no distro info")
	}
	if len(cheapImg.Tags) != len(fullImg.Tags) {
		t.Fatalf("Tags = %v, want %v (byte-parity with the full resolve)", cheapImg.Tags, fullImg.Tags)
	}
	for i := range fullImg.Tags {
		if cheapImg.Tags[i] != fullImg.Tags[i] {
			t.Errorf("Tags[%d] = %q, want %q", i, cheapImg.Tags[i], fullImg.Tags[i])
		}
	}
}
