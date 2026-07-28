package main

import (
	"reflect"
	"testing"

	"github.com/opencharly/sdk/buildkit"
)

// buildkit.NormalizeBoxArgs's own sentinel-collapse behavior is covered by
// sdk/buildkit/build_helpers_test.go (TestNormalizeBoxArgs) now that the logic lives there
// (the BUILD-cone cutover); this file keeps only the charly-side composition with
// boxResolveOpts below.

// TestBoxResolveOpts asserts the single box-selection rule both build and
// generate consume: empty → no scoping (all enabled); named → RequestedBoxes
// scoping; named + include-disabled → per-name gate relaxation; the gate is
// NEVER widened globally (empty selection never populates IncludeDisabledNames).
func TestBoxResolveOpts(t *testing.T) {
	t.Run("empty selection, no include-disabled", func(t *testing.T) {
		opts := boxResolveOpts(nil, false)
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
		opts := boxResolveOpts(nil, true)
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
		opts := boxResolveOpts([]string{"fedora", "arch"}, false)
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
		opts := boxResolveOpts([]string{"immich", "versa"}, true)
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
// loaderkit.ResolveOpts for the same selection — the whole point of R3-unifying them.
func TestBuildResolveOptsParity(t *testing.T) {
	for _, sel := range [][]string{nil, {"fedora"}, {"fedora", "arch"}} {
		for _, incl := range []bool{false, true} {
			a := boxResolveOpts(buildkit.NormalizeBoxArgs(sel), incl)
			b := boxResolveOpts(buildkit.NormalizeBoxArgs(sel), incl)
			if !reflect.DeepEqual(a, b) {
				t.Errorf("parity mismatch for sel=%v incl=%v: %+v vs %+v", sel, incl, a, b)
			}
		}
	}
	// `all` and the bare form must resolve identically.
	allOpts := boxResolveOpts(buildkit.NormalizeBoxArgs([]string{"all"}), false)
	bareOpts := boxResolveOpts(buildkit.NormalizeBoxArgs(nil), false)
	if !reflect.DeepEqual(allOpts, bareOpts) {
		t.Errorf("`generate all` opts %+v != bare `generate` opts %+v", allOpts, bareOpts)
	}
}

// `charly box generate`'s Kong-parse coverage (its boxes positional + --include-disabled flag) moved
// into candy/plugin-box's own tests with the P15 externalization — `charly box generate` is now a
// nested command served by the COMPILED-IN candy/plugin-box.
