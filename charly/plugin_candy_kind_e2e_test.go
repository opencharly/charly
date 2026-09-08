package main

import (
	"testing"

	"github.com/opencharly/spec/spec"
)

// TestCandyKind_BothShapesByteEquivalent is the C2-candy acceptance proof: the COMPILED-IN
// candy/plugin-candy-kind seam (foldCandyKind → host pre-decode CandyIsImage+BuildCandy → plugin
// ECHO → fold) produces a result BYTE-EQUIVALENT to a DIRECT core decode of the same node, for
// BOTH candy shapes:
//
//   - a full IMAGE (base:) folds into uf.Box, byte-identical to pl.DecodeNodeValue(pn, &BoxConfig)
//     (the former in-proc candyKind image path); and
//   - a LAYER fragment folds into uf.Candy, byte-identical to pl.BuildCandy(pn) (the former
//     in-proc candyKind layer path).
//
// Both baselines run on the PARSED node through the loader seam (parser consolidation F2.1: the
// genericNode bridge + its yaml-scan baselines are gone). RDD proved a canonical spec.Box /
// spec.Candy round-trips through JSON byte-faithfully; this locks it through the REAL compiled-in
// plugin provider (providerRegistry.ResolveKind). candy is THE core entity, so this + box validate
// across all repos are the acceptance gate. Compiled-in, so NOT -short-gated (no external build).
func TestCandyKind_BothShapesByteEquivalent(t *testing.T) {
	prov, ok := providerRegistry.ResolveKind("candy")
	if !ok {
		t.Fatal("candy kind must resolve to the compiled-in candy/plugin-candy-kind provider")
	}
	pl := requireProjectLoader()

	// --- IMAGE shape (base: → uf.Box) ---
	imgPn := singleParsedNode(t, IMG_DOC)
	if !pl.CandyIsImage(imgPn) {
		t.Fatal("CandyIsImage must report the base:-carrying node as an image")
	}
	var accImg spec.MaterializedProject
	if err := foldCandyKind(prov, imgPn, &accImg); err != nil {
		t.Fatalf("foldCandyKind (image): %v", err)
	}
	bc, ok := spec.BoxConfigFrom(accImg.Box, "my-image")
	if !ok {
		t.Fatalf("image shape not folded into acc.Box; boxes=%v", boxKeys(accImg.Box))
	}
	if accImg.Candy["my-image"] != nil {
		t.Fatal("image shape also landed in acc.Candy — must be acc.Box ONLY")
	}
	var baseBox spec.BoxConfig
	if err := pl.DecodeNodeValue(imgPn, &baseBox); err != nil {
		t.Fatalf("baseline DecodeNodeValue (image): %v", err)
	}
	if got, want := mustJSON(t, bc), mustJSON(t, baseBox); got != want {
		t.Fatalf("IMAGE-shape plugin fold != direct core decode\n plugin: %s\n core:   %s", got, want)
	}

	// --- LAYER shape (no base/from → uf.Candy) ---
	layerPn := singleParsedNode(t, LAYER_DOC)
	if pl.CandyIsImage(layerPn) {
		t.Fatal("CandyIsImage must report the base/from-less node as a layer")
	}
	var accLayer spec.MaterializedProject
	if err := foldCandyKind(prov, layerPn, &accLayer); err != nil {
		t.Fatalf("foldCandyKind (layer): %v", err)
	}
	ic, ok := spec.DecodeInlineCandy(accLayer.Candy["my-layer"])
	if !ok {
		t.Fatalf("layer shape not folded into acc.Candy; candies=%v", mapKeys(accLayer.Candy))
	}
	if _, dup := accLayer.Box["my-layer"]; dup {
		t.Fatal("layer shape also landed in acc.Box — must be acc.Candy ONLY")
	}
	_, baseIc, err := pl.BuildCandy(layerPn)
	if err != nil {
		t.Fatalf("baseline BuildCandy (layer): %v", err)
	}
	if got, want := mustJSON(t, ic), mustJSON(t, baseIc); got != want {
		t.Fatalf("LAYER-shape plugin fold != direct core decode\n plugin: %s\n core:   %s", got, want)
	}
}

const IMG_DOC = "my-image:\n" +
	"    candy:\n" +
	"        base: fedora\n" +
	"        version: \"2026.150.0000\"\n" +
	"        candy:\n" +
	"            - redis\n"

const LAYER_DOC = "my-layer:\n" +
	"    candy:\n" +
	"        version: \"2026.150.0000\"\n" +
	"        description: a layer\n" +
	"        package:\n" +
	"            - git\n" +
	"        plan:\n" +
	"            - run: install a marker\n" +
	"              command: \"true\"\n" +
	"              run_as: root\n" +
	"            - check: the marker exists\n" +
	"              command: \"true\"\n"
