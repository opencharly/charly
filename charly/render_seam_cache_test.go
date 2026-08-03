package main

import (
	"testing"

	"github.com/opencharly/spec/spec"
)

// render_seam_cache_test.go — the regression for the K3 host-prep move (coneB-render): a first cut
// of newCandyScanGenerator dropped the box-resolve pass entirely, leaving gen.Boxes empty. That
// broke host_build_render_seam.go's RenderSeamInlineBuilder case (renderSeamGenBox's gen.Boxes[boxName]
// lookup always "not found") — invisible to the `charly box generate` byte-parity smoke test, which
// never exercises the inline-builder reverse-channel seam. Caught by tracing every reader of the
// cached Generator (per the team-lead's watch-point), not by the box-generate smoke test.
//
// K3 cone2 test closure, split by assertion (mirroring candy/plugin-build/namespace_resolve_test.go's
// documented precedent: "a test asserting ResolveBox/ResolveAllBox OUTPUT is resolver-capability
// coverage... what the loader PRODUCED stays charly's to test, what ResolveBox MAKES of it is this
// plugin's"). The former single test bundled two concerns: (a) does buildkit.ResolveAllBox/ResolveBox
// produce sane, mutually-consistent Tags for a real builder-based image (a resolver-OUTPUT claim) —
// moved to candy/plugin-build/box_resolve_test.go's TestResolveAllBox_ResolveBoxParity with a literal
// *spec.Config fixture in place of a real box/fedora disk read; (b) does newCandyScanGenerator STORE
// the caller-pushed box set verbatim, keyed by name, without dropping fields (a cache-BEHAVIOR
// claim — the actual regression this file guards) — kept here, now with a literal fabricated
// *spec.ResolvedBox fixture instead of a real buildkit.ResolveAllBox/box-fedora round trip, since
// newCandyScanGenerator stores its `boxes` argument verbatim (charly/generate.go's
// `Boxes: boxes`) with no validation against the loaded project's own box set.

// TestNewCandyScanGeneratorPopulatesBoxes proves the cheap render-seam-floor constructor STORES
// the caller-pushed box set (needed for resolveInlineBuilderSeam's img.Tags/img.Name — see
// resolveBuilderStage) verbatim — a literal *spec.ResolvedBox fixture round-trips through
// newCandyScanGenerator's `boxes` parameter into `cheap.Boxes[name]` unchanged. #55 coneB2 Class B:
// the boxes are PUSHED (mimicking candy/plugin-build's resolveBuildEngine — buildkit.ResolveAllBox +
// the &b.ResolvedBox projection deploykit.SpecBoxes performs in production) rather than
// self-resolved; the constructor itself no longer imports deploykit, so this test needs no
// buildkit/deploykit import either — it only exercises the STORE, not the resolve.
func TestNewCandyScanGeneratorPopulatesBoxes(t *testing.T) {
	t.Cleanup(snapshotProviderState())

	const boxName = "fedora-builder"
	pushed := &spec.ResolvedBox{Name: boxName, Tags: []string{"fedora", "rpm"}}
	specBoxes := map[string]*spec.ResolvedBox{boxName: pushed}

	cheap, err := newCandyScanGenerator(testdataDir, false, nil, specBoxes)
	if err != nil {
		t.Fatalf("newCandyScanGenerator: %v", err)
	}
	cheapImg := cheap.Boxes[boxName]
	if cheapImg == nil {
		t.Fatalf("newCandyScanGenerator: box %q not found in Boxes — the render-seam floor's "+
			"RenderSeamInlineBuilder case would fail 'box not found' for every request", boxName)
	}
	if cheapImg != pushed {
		t.Fatalf("newCandyScanGenerator: Boxes[%q] = %v, want the exact pushed *spec.ResolvedBox (stored verbatim, no copy/re-resolve)", boxName, cheapImg)
	}
	if cheapImg.Name != boxName {
		t.Errorf("Name = %q, want %q", cheapImg.Name, boxName)
	}
	if len(cheapImg.Tags) == 0 {
		t.Fatalf("Tags is empty — resolveBuilderStage's spec.BuildEnv{Distros: img.Tags} would carry no distro info")
	}
}
