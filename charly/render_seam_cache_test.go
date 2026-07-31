package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/spec/spec"
)

// render_seam_cache_test.go — the regression for the K3 host-prep move (coneB-render): a first cut
// of newCandyScanGenerator dropped the box-resolve pass entirely, leaving gen.Boxes empty. That
// broke host_build_render_seam.go's RenderSeamInlineBuilder case (renderSeamGenBox's gen.Boxes[boxName]
// lookup always "not found") — invisible to the `charly box generate` byte-parity smoke test, which
// never exercises the inline-builder reverse-channel seam. Caught by tracing every reader of the
// cached Generator (per the team-lead's watch-point), not by the box-generate smoke test.
//
// #55 step3 3-II: the former "full NewGenerator" comparison baseline is GONE — NewGenerator itself
// is deleted (its last production caller, the pod-overlay seam, now reaches the SAME render-prepped
// resolve plugin-side via candy/plugin-build's resolveBuildEngine, a separate Go module this
// host-side test cannot call into). The assertion below is rewritten to check
// newCandyScanGenerator's output directly against a fresh buildkit.ResolveBox call — the SAME
// primitive it wraps — rather than diffing two Generators.

// TestNewCandyScanGeneratorPopulatesBoxes proves the cheap render-seam-floor constructor STORES
// the caller-pushed box set (needed for resolveInlineBuilderSeam's img.Tags/img.Name — see
// resolveBuilderStage) with the SAME Name/Tags a direct buildkit.ResolveBox call would produce,
// using a real project fixture (box/fedora's "fedora-builder", a builder-based image exercising a
// real distro/tags resolve). #55 coneB2 Class B: the boxes are now PUSHED (mimicking
// candy/plugin-build's resolveBuildEngine — buildkit.ResolveAllBox + the &b.ResolvedBox projection
// that deploykit.SpecBoxes performs in production) rather than self-resolved via
// deploykit.ResolveAllSpecBoxes; the constructor itself no longer imports deploykit.
func TestNewCandyScanGeneratorPopulatesBoxes(t *testing.T) {
	repoRoot, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(filepath.Dir(repoRoot), "box", "fedora")
	const boxName = "fedora-builder"

	t.Cleanup(snapshotProviderState())

	// Resolve the box set the way plugin-build does (buildkit.ResolveAllBox + the spec projection).
	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	distroCfg, _, _, err := LoadDefaultBuildConfig(dir)
	if err != nil {
		t.Fatalf("LoadDefaultBuildConfig: %v", err)
	}
	RegisterBuildVocabulary(distroCfg)
	resolved, err := buildkit.ResolveAllBox(cfg, "", dir, buildkit.ResolveOpts{})
	if err != nil {
		t.Fatalf("buildkit.ResolveAllBox: %v", err)
	}
	specBoxes := make(map[string]*spec.ResolvedBox, len(resolved))
	for name, b := range resolved {
		if b == nil {
			continue
		}
		specBoxes[name] = &b.ResolvedBox
	}

	cheap, err := newCandyScanGenerator(dir, false, nil, specBoxes)
	if err != nil {
		t.Fatalf("newCandyScanGenerator: %v", err)
	}
	cheapImg := cheap.Boxes[boxName]
	if cheapImg == nil {
		t.Fatalf("newCandyScanGenerator: box %q not found in Boxes — the render-seam floor's "+
			"RenderSeamInlineBuilder case would fail 'box not found' for every request", boxName)
	}
	if cheapImg.Name != boxName {
		t.Errorf("Name = %q, want %q", cheapImg.Name, boxName)
	}
	if len(cheapImg.Tags) == 0 {
		t.Fatalf("Tags is empty — resolveBuilderStage's spec.BuildEnv{Distros: img.Tags} would carry no distro info")
	}

	direct, err := buildkit.ResolveBox(cfg, boxName, "", dir, buildkit.ResolveOpts{})
	if err != nil {
		t.Fatalf("buildkit.ResolveBox: %v", err)
	}
	if len(direct.Tags) != len(cheapImg.Tags) {
		t.Fatalf("Tags = %v, want %v (parity with a direct buildkit.ResolveBox call)", cheapImg.Tags, direct.Tags)
	}
	for i := range direct.Tags {
		if cheapImg.Tags[i] != direct.Tags[i] {
			t.Errorf("Tags[%d] = %q, want %q", i, cheapImg.Tags[i], direct.Tags[i])
		}
	}
}
