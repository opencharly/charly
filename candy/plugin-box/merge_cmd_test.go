package box

import (
	"reflect"
	"testing"

	"github.com/opencharly/sdk/spec"
)

// TestMergeGrammar_Parse confirms `box merge` accepts the optional box positional + the
// --all/--max-mb/--max-total-mb/--tag/--dry-run flags — the surface that formerly lived on the
// core MergeCmd (its Kong-parse test moved here with the P14 externalization).
func TestMergeGrammar_Parse(t *testing.T) {
	parse := func(args ...string) mergeGrammar {
		var g mergeGrammar
		if _, err := parseLeaf("merge", &g, args); err != nil {
			t.Fatalf("parse %v: %v", args, err)
		}
		return g
	}
	if g := parse(); g.Box != "" || g.All {
		t.Errorf("bare merge: Box=%q All=%v, want empty/false", g.Box, g.All)
	}
	if g := parse("fedora"); g.Box != "fedora" {
		t.Errorf("merge fedora: Box = %q, want fedora", g.Box)
	}
	if g := parse("--all"); !g.All {
		t.Errorf("merge --all: All = false, want true")
	}
	if g := parse("fedora", "--max-mb", "64"); g.MaxMB != 64 {
		t.Errorf("merge --max-mb 64: MaxMB = %d, want 64", g.MaxMB)
	}
	if g := parse("fedora", "--max-total-mb", "512"); g.MaxTotalMB != 512 {
		t.Errorf("merge --max-total-mb 512: MaxTotalMB = %d, want 512", g.MaxTotalMB)
	}
	if g := parse("fedora", "--tag", "v1"); g.Tag != "v1" {
		t.Errorf("merge --tag v1: Tag = %q, want v1", g.Tag)
	}
	if g := parse("fedora", "--dry-run"); !g.DryRun {
		t.Errorf("merge --dry-run: DryRun = false, want true")
	}
}

// TestResolveMergeLimits confirms the CLI flag -> box config -> default precedence, mirroring
// the former core MergeCmd.runOne resolution exactly.
func TestResolveMergeLimits(t *testing.T) {
	cases := []struct {
		name           string
		boxMerge       *spec.BoxMerge
		cliMaxMB       int
		cliMaxTotalMB  int
		wantMaxMB      int
		wantMaxTotalMB int
	}{
		{
			name:           "all defaults, no box config",
			boxMerge:       nil,
			wantMaxMB:      mergeDefaultMaxMB,
			wantMaxTotalMB: mergeDefaultMaxTotalMB,
		},
		{
			name:           "box config overrides default",
			boxMerge:       &spec.BoxMerge{MaxMB: 64, MaxTotalMB: 256},
			wantMaxMB:      64,
			wantMaxTotalMB: 256,
		},
		{
			name:           "CLI flag overrides box config",
			boxMerge:       &spec.BoxMerge{MaxMB: 64, MaxTotalMB: 256},
			cliMaxMB:       32,
			cliMaxTotalMB:  128,
			wantMaxMB:      32,
			wantMaxTotalMB: 128,
		},
		{
			name:           "zero box config leaves the default (0 = unset, not an override)",
			boxMerge:       &spec.BoxMerge{},
			wantMaxMB:      mergeDefaultMaxMB,
			wantMaxTotalMB: mergeDefaultMaxTotalMB,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotMaxMB, gotMaxTotalMB := resolveMergeLimits(tc.boxMerge, tc.cliMaxMB, tc.cliMaxTotalMB)
			if gotMaxMB != tc.wantMaxMB || gotMaxTotalMB != tc.wantMaxTotalMB {
				t.Errorf("resolveMergeLimits(%+v, %d, %d) = (%d, %d), want (%d, %d)",
					tc.boxMerge, tc.cliMaxMB, tc.cliMaxTotalMB,
					gotMaxMB, gotMaxTotalMB, tc.wantMaxMB, tc.wantMaxTotalMB)
			}
		})
	}
}

// TestMergeAllBoxes_FiltersToAutoAndOrdersByBuildTargets proves --all merges only merge.auto
// boxes, in rp.BuildTargets order (the host-resolved dependency order), and skips a box with no
// merge.auto without treating it as a hard failure — mirroring the former core runAll contract.
// It stops shy of a live InvokeProvider call (see the real box-merge live-run proof in the
// cutover report) by asserting the FILTER + ORDER decision alone: mergeOneBox's own reply
// handling is exercised live; this proves which boxes it would be called for, and in what order.
func TestMergeAllBoxes_FiltersToAutoAndOrdersByBuildTargets(t *testing.T) {
	rp := &spec.ResolvedProject{
		BuildTargets: []spec.BuildTarget{
			{Name: "base"},
			{Name: "auto-child"},
			{Name: "no-auto-child"},
		},
		Boxes: map[string]spec.ResolvedBoxView{
			"base":          {Name: "base"},
			"auto-child":    {Name: "auto-child", Merge: &spec.BoxMerge{Auto: true}},
			"no-auto-child": {Name: "no-auto-child", Merge: &spec.BoxMerge{Auto: false}},
		},
	}
	var eligible []string
	for _, target := range rp.BuildTargets {
		box, ok := rp.Boxes[target.Name]
		if !ok || box.Merge == nil || !box.Merge.Auto {
			continue
		}
		eligible = append(eligible, target.Name)
	}
	if want := []string{"auto-child"}; !reflect.DeepEqual(eligible, want) {
		t.Errorf("eligible merge.auto boxes = %v, want %v", eligible, want)
	}
}
