package spec

import (
	"reflect"
	"strings"
	"testing"
)

// buildkit.NormalizeBoxArgs's own sentinel-collapse behavior is covered by
// sdk/buildkit/build_helpers_test.go (TestNormalizeBoxArgs) now that the logic lives there
// (the BUILD-cone cutover); this file keeps only the spec-side composition with
// BoxResolveOpts below. normalizeBoxArgsFixture is a trivial LOCAL fixture reproducing that
// same sentinel-collapse (not a re-test of NormalizeBoxArgs itself) so this file needs no
// sdk/buildkit import for what is, here, pure test input construction.
func normalizeBoxArgsFixture(boxes []string) []string {
	if len(boxes) == 1 && strings.EqualFold(boxes[0], "all") {
		return nil
	}
	return boxes
}

// TestBoxResolveOpts asserts the single box-selection rule both build and
// generate consume: empty → no scoping (all enabled); named → RequestedBoxes
// scoping; named + include-disabled → per-name gate relaxation; the gate is
// NEVER widened globally (empty selection never populates IncludeDisabledNames).
func TestBoxResolveOpts(t *testing.T) {
	t.Run("empty selection, no include-disabled", func(t *testing.T) {
		opts := BoxResolveOpts(nil, false)
		if opts.IncludeDisabled {
			t.Errorf("IncludeDisabled = true, want false")
		}
		if opts.RequestedBoxes != nil {
			t.Errorf("RequestedBoxes = %v, want nil", opts.RequestedBoxes)
		}
		if opts.IncludeDisabledNames != nil {
			t.Errorf("IncludeDisabledNames = %v, want nil", opts.IncludeDisabledNames)
		}
	})

	t.Run("empty selection with include-disabled widens globally, not per-name", func(t *testing.T) {
		opts := BoxResolveOpts(nil, true)
		if !opts.IncludeDisabled {
			t.Errorf("IncludeDisabled = false, want true")
		}
		if opts.RequestedBoxes != nil {
			t.Errorf("RequestedBoxes = %v, want nil", opts.RequestedBoxes)
		}
		// No names → IncludeDisabledNames stays nil so the gate relaxes globally
		// (the documented `charly box build --include-disabled` no-arg behaviour).
		if opts.IncludeDisabledNames != nil {
			t.Errorf("IncludeDisabledNames = %v, want nil (global relaxation)", opts.IncludeDisabledNames)
		}
	})

	t.Run("named selection scopes RequestedBoxes only", func(t *testing.T) {
		opts := BoxResolveOpts([]string{"fedora", "arch"}, false)
		if !reflect.DeepEqual(opts.RequestedBoxes, []string{"fedora", "arch"}) {
			t.Errorf("RequestedBoxes = %v, want [fedora arch]", opts.RequestedBoxes)
		}
		if opts.IncludeDisabled {
			t.Errorf("IncludeDisabled = true, want false")
		}
		if opts.IncludeDisabledNames != nil {
			t.Errorf("IncludeDisabledNames = %v, want nil (no --include-disabled)", opts.IncludeDisabledNames)
		}
	})

	t.Run("named selection with include-disabled scopes the gate to those names", func(t *testing.T) {
		opts := BoxResolveOpts([]string{"immich", "versa"}, true)
		if !opts.IncludeDisabled {
			t.Errorf("IncludeDisabled = false, want true")
		}
		if !reflect.DeepEqual(opts.RequestedBoxes, []string{"immich", "versa"}) {
			t.Errorf("RequestedBoxes = %v, want [immich versa]", opts.RequestedBoxes)
		}
		want := map[string]bool{"immich": true, "versa": true}
		if !reflect.DeepEqual(opts.IncludeDisabledNames, want) {
			t.Errorf("IncludeDisabledNames = %v, want %v", opts.IncludeDisabledNames, want)
		}
	})
}

// TestBuildResolveOptsParity locks in that build and generate produce the SAME
// spec.ResolveOpts for the same selection — the whole point of R3-unifying them.
func TestBuildResolveOptsParity(t *testing.T) {
	for _, sel := range [][]string{nil, {"fedora"}, {"fedora", "arch"}} {
		for _, incl := range []bool{false, true} {
			a := BoxResolveOpts(normalizeBoxArgsFixture(sel), incl)
			b := BoxResolveOpts(normalizeBoxArgsFixture(sel), incl)
			if !reflect.DeepEqual(a, b) {
				t.Errorf("parity mismatch for sel=%v incl=%v: %+v vs %+v", sel, incl, a, b)
			}
		}
	}
	// `all` and the bare form must resolve identically.
	allOpts := BoxResolveOpts(normalizeBoxArgsFixture([]string{"all"}), false)
	bareOpts := BoxResolveOpts(normalizeBoxArgsFixture(nil), false)
	if !reflect.DeepEqual(allOpts, bareOpts) {
		t.Errorf("`generate all` opts %+v != bare `generate` opts %+v", allOpts, bareOpts)
	}
}

// `charly box generate`'s Kong-parse coverage (its boxes positional + --include-disabled flag) moved
// into candy/plugin-box's own tests with the P15 externalization — `charly box generate` is now a
// nested command served by the COMPILED-IN candy/plugin-box.

// TestWithLocalRawRefs asserts the augmentation that makes a local candy's PINNED remote dep
// discoverable by the reachability walk. It carried NO coverage while it lived as charly's private
// withLocalRawRefs; the K-wave 2 cone R1 (A2) relocation added it, because candy/plugin-build now
// calls this directly from its own CollectRemoteRefs leg — a silent regression here reproduces the
// "depends: unknown candy" crash the function exists to prevent, and nothing else would catch it.
func TestWithLocalRawRefs(t *testing.T) {
	scanned := map[string]ScannedCandy{
		"local-a": {Refs: CandyRefs{
			// The RAW pin must survive verbatim — a bare name never looks remote to the walk.
			Require:       []CandyRefEntry{{Raw: "@github.com/opencharly/charly/candy/plug:v2026.1.1"}},
			IncludedCandy: []CandyRefEntry{{Raw: "sibling-local"}},
			// BakePlugin is deliberately NOT harvested (only require:/candy: are).
			BakePlugin: []CandyRefEntry{{Raw: "@github.com/opencharly/charly/candy/baked:v1"}},
		}},
	}

	t.Run("raw pins are appended to ExtraCandyRefs", func(t *testing.T) {
		got := WithLocalRawRefs(ResolveOpts{}, scanned)
		want := map[string]bool{
			"@github.com/opencharly/charly/candy/plug:v2026.1.1": true,
			"sibling-local": true,
		}
		if len(got.ExtraCandyRefs) != len(want) {
			t.Fatalf("ExtraCandyRefs = %v, want the %d require:/candy: raws", got.ExtraCandyRefs, len(want))
		}
		for _, r := range got.ExtraCandyRefs {
			if !want[r] {
				t.Errorf("unexpected harvested ref %q (bake_plugin: must NOT be harvested)", r)
			}
		}
	})

	t.Run("pre-existing ExtraCandyRefs are preserved, input opts untouched", func(t *testing.T) {
		in := ResolveOpts{ExtraCandyRefs: []string{"add-candy-ref"}}
		got := WithLocalRawRefs(in, scanned)
		if len(in.ExtraCandyRefs) != 1 {
			t.Errorf("input opts mutated: %v (must copy, never append in place)", in.ExtraCandyRefs)
		}
		if got.ExtraCandyRefs[0] != "add-candy-ref" {
			t.Errorf("ExtraCandyRefs[0] = %q, want the pre-existing add_candy ref first", got.ExtraCandyRefs[0])
		}
		if len(got.ExtraCandyRefs) != 3 {
			t.Errorf("ExtraCandyRefs = %v, want 1 pre-existing + 2 harvested", got.ExtraCandyRefs)
		}
	})

	t.Run("empty scan is a no-op", func(t *testing.T) {
		got := WithLocalRawRefs(ResolveOpts{IncludeDisabled: true}, nil)
		if len(got.ExtraCandyRefs) != 0 {
			t.Errorf("ExtraCandyRefs = %v, want empty", got.ExtraCandyRefs)
		}
		if !got.IncludeDisabled {
			t.Errorf("IncludeDisabled dropped by the augmentation")
		}
	})
}
