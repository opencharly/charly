package fleet

import (
	"reflect"
	"testing"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/spec/spec"
)

// box_select_test.go — the ADD-CANDY-ON-BOX shape parity guard (K4 box-half completion). The
// compileCandyOnBoxSelection host path (buildkit.ResolveBox(baseImg) + scanCandiesForRef) was
// deleted and its work moved into resolveAddCandyOnBoxSelection, which resolves BOTH halves off the
// resolved-project envelope. This test proves that move is a faithful COMPOSITION of the two
// already-parity-proven envelope resolvers (the BOX-REF shape's rp.Boxes read + the CANDY shape's
// ResolveCandyOrder), not a new/divergent resolution:
//
//	(1) BASE parity — resolveAddCandyOnBoxSelection's base image is BYTE-IDENTICAL to
//	    resolveBoxSelection's for the SAME base box (both call NewSpecResolvedBox(rp.Boxes[name],
//	    rp.Distro, rp.Builder)); a regression that read the base differently would fail here. This is
//	    the exact base-equivalence the deleted host-side buildkit.ResolveBox(refStr) relied on, now
//	    proven against the envelope directly.
//	(2) ORDER parity — the overlay candy's topo order is exactly ResolveCandyOrder over the SAME
//	    envelopeCandyModels the CANDY/BOX-REF shapes use (the deleted host scanCandiesForRef +
//	    ResolveCandyOrder([candyKey], layers) equivalent).
//	(3) error surfaces — a missing base box / missing overlay candy is a loud error, never a silent
//	    empty compile (the K1-alpha "candy not in resolved-project envelope" failure class).
func TestResolveAddCandyOnBoxSelection_ComposesProvenResolvers(t *testing.T) {
	rp := &spec.ResolvedProject{
		Boxes: map[string]spec.ResolvedBoxView{
			"base": {
				Name:         "base",
				Home:         "/home/user",
				User:         "user",
				UID:          1000,
				GID:          1000,
				Distro:       []string{"fedora:43", "fedora"},
				BuildFormats: []string{"rpm"},
				Pkg:          "rpm",
				Candy:        []string{"baseonly"},
			},
		},
		// Two leaf candies: "baseonly" is the base box's own candy; "overlay" is the add_candy target.
		Candies: map[string]spec.CandyView{
			"baseonly": {Name: "baseonly", Version: "2026.001.0001"},
			"overlay":  {Name: "overlay", Version: "2026.001.0002"},
		},
		CandyModels: map[string]spec.CandyModel{
			"baseonly": {Name: "baseonly", Version: "2026.001.0001"},
			"overlay":  {Name: "overlay", Version: "2026.001.0002"},
		},
		Distro:  map[string]*spec.ResolvedDistro{"fedora": {Format: map[string]*spec.Format{"rpm": {}}}},
		Builder: map[string]*spec.Builder{},
	}

	// (1) BASE parity: the ADD-CANDY-ON-BOX base == the BOX-REF shape's base for the same box.
	addOrder, addImg, err := resolveAddCandyOnBoxSelection(rp, spec.DeployCompileRequest{BaseBoxRef: "base", CandyRef: "overlay"})
	if err != nil {
		t.Fatalf("resolveAddCandyOnBoxSelection: %v", err)
	}
	boxImg, _, err := resolveBoxSelection(rp, spec.DeployCompileRequest{BoxRef: "base"})
	if err != nil {
		t.Fatalf("resolveBoxSelection: %v", err)
	}
	if !reflect.DeepEqual(addImg, boxImg) {
		t.Fatalf("BASE parity break: ADD-CANDY-ON-BOX base image differs from the BOX-REF shape's base for the same box\n add=%+v\n box=%+v", addImg, boxImg)
	}

	// (2) ORDER parity: the overlay order == ResolveCandyOrder over the SAME envelopeCandyModels.
	wantOrder, err := deploykit.ResolveCandyOrder([]string{"overlay"}, envelopeCandyModels(rp), nil)
	if err != nil {
		t.Fatalf("ResolveCandyOrder(overlay): %v", err)
	}
	if !reflect.DeepEqual(addOrder, wantOrder) {
		t.Fatalf("ORDER parity break: got %v, want %v (the overlay's own topo order, NOT the base box's)", addOrder, wantOrder)
	}
	// The overlay order must be the OVERLAY's closure, never the base box's own candy ("baseonly").
	for _, c := range addOrder {
		if c == "baseonly" {
			t.Fatalf("ORDER leak: add-candy-on-box compiled the base box's own candy %q — it must compile ONLY the overlay's closure (%v)", c, addOrder)
		}
	}

	// (3) error surfaces — missing base box, missing overlay candy.
	if _, _, err := resolveAddCandyOnBoxSelection(rp, spec.DeployCompileRequest{BaseBoxRef: "nope", CandyRef: "overlay"}); err == nil {
		t.Error("expected an error for a base box absent from the envelope; got nil")
	}
	if _, _, err := resolveAddCandyOnBoxSelection(rp, spec.DeployCompileRequest{BaseBoxRef: "base", CandyRef: "nope"}); err == nil {
		t.Error("expected an error for an overlay candy absent from the envelope; got nil")
	}
}
