package main

import (
	"reflect"
	"testing"

	"github.com/alecthomas/kong"
)

// TestNormalizeBoxArgs asserts the `all` sentinel collapses to nil ONLY when it
// is the sole argument — the canonical "every enabled box" shape shared by
// `charly box build` and `charly box generate`.
func TestNormalizeBoxArgs(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"nil stays nil", nil, nil},
		{"empty stays empty", []string{}, []string{}},
		{"lone all → nil", []string{"all"}, nil},
		{"lone ALL (case-insensitive) → nil", []string{"ALL"}, nil},
		{"lone All → nil", []string{"All"}, nil},
		{"single named box passes through", []string{"fedora"}, []string{"fedora"}},
		{"all alongside another name is literal", []string{"all", "fedora"}, []string{"all", "fedora"}},
		{"two named boxes pass through", []string{"fedora", "arch"}, []string{"fedora", "arch"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeBoxArgs(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("normalizeBoxArgs(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

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
// ResolveOpts for the same selection — the whole point of R3-unifying them.
func TestBuildResolveOptsParity(t *testing.T) {
	for _, sel := range [][]string{nil, {"fedora"}, {"fedora", "arch"}} {
		for _, incl := range []bool{false, true} {
			a := boxResolveOpts(normalizeBoxArgs(sel), incl)
			b := boxResolveOpts(normalizeBoxArgs(sel), incl)
			if !reflect.DeepEqual(a, b) {
				t.Errorf("parity mismatch for sel=%v incl=%v: %+v vs %+v", sel, incl, a, b)
			}
		}
	}
	// `all` and the bare form must resolve identically.
	allOpts := boxResolveOpts(normalizeBoxArgs([]string{"all"}), false)
	bareOpts := boxResolveOpts(normalizeBoxArgs(nil), false)
	if !reflect.DeepEqual(allOpts, bareOpts) {
		t.Errorf("`generate all` opts %+v != bare `generate` opts %+v", allOpts, bareOpts)
	}
}

// TestGenerateCmdKongParse confirms `charly box generate` now accepts the
// optional box positional and the --include-disabled flag (the new surface),
// and still parses with no arguments (the default-all path).
func TestGenerateCmdKongParse(t *testing.T) {
	parse := func(args ...string) GenerateCmd {
		var cli struct {
			Generate GenerateCmd `cmd:""`
		}
		p, err := kong.New(&cli)
		if err != nil {
			t.Fatalf("kong.New: %v", err)
		}
		if _, err := p.Parse(args); err != nil {
			t.Fatalf("parse %v: %v", args, err)
		}
		return cli.Generate
	}

	if g := parse("generate"); len(g.Boxes) != 0 {
		t.Errorf("bare generate: Boxes = %v, want empty", g.Boxes)
	}
	if g := parse("generate", "all"); !reflect.DeepEqual(g.Boxes, []string{"all"}) {
		t.Errorf("generate all: Boxes = %v, want [all]", g.Boxes)
	}
	if g := parse("generate", "fedora"); !reflect.DeepEqual(g.Boxes, []string{"fedora"}) {
		t.Errorf("generate fedora: Boxes = %v, want [fedora]", g.Boxes)
	}
	if g := parse("generate", "fedora", "arch"); !reflect.DeepEqual(g.Boxes, []string{"fedora", "arch"}) {
		t.Errorf("generate fedora arch: Boxes = %v, want [fedora arch]", g.Boxes)
	}
	if g := parse("generate", "immich", "--include-disabled"); !g.IncludeDisabled {
		t.Errorf("generate immich --include-disabled: IncludeDisabled = false, want true")
	}
}
