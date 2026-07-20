package main

import (
	"testing"

	"github.com/opencharly/sdk/spec"

	"gopkg.in/yaml.v3"
)

// candyNodeFromYAML parses a single-entity node-form doc and returns its top-level genericNode
// (for the C2-candy byte-equivalence proof).
func candyNodeFromYAML(t *testing.T, doc string) *genericNode {
	t.Helper()
	var ydoc yaml.Node
	if err := yaml.Unmarshal([]byte(doc), &ydoc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	nodes, err := genericNodesFromDoc(&ydoc)
	if err != nil {
		t.Fatalf("genericNodesFromDoc: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("want 1 node, got %d", len(nodes))
	}
	return nodes[0]
}

// TestCandyKind_BothShapesByteEquivalent is the C2-candy acceptance proof: the COMPILED-IN
// candy/plugin-candy-kind seam (foldCandyKind → host pre-decode candyIsImage+buildCandy → plugin
// ECHO → fold) produces a result BYTE-EQUIVALENT to a DIRECT core decode of the same node, for BOTH
// candy shapes:
//
//   - a full IMAGE (base:) folds into uf.Box, byte-identical to decodeNodeValue(gn, &BoxConfig)
//     (the former in-proc candyKind image path); and
//   - a LAYER fragment folds into uf.Candy, byte-identical to buildCandy(gn) (the former in-proc
//     candyKind layer path).
//
// RDD proved a canonical spec.Box / spec.Candy round-trips through JSON byte-faithfully; this locks
// it through the REAL compiled-in plugin provider (providerRegistry.ResolveKind). candy is THE core
// entity, so this + box validate across all repos are the acceptance gate. Compiled-in, so NOT
// -short-gated (no external build).
func TestCandyKind_BothShapesByteEquivalent(t *testing.T) {
	prov, ok := providerRegistry.ResolveKind("candy")
	if !ok {
		t.Fatal("candy kind must resolve to the compiled-in candy/plugin-candy-kind provider")
	}

	// --- IMAGE shape (base: → uf.Box) ---
	imgDoc := `my-image:
    candy:
        base: fedora
        version: "2026.150.0000"
        candy:
            - redis
`
	imgGn := candyNodeFromYAML(t, imgDoc)
	var ufImg UnifiedFile
	if err := foldCandyKind(prov, imgGn, &ufImg); err != nil {
		t.Fatalf("foldCandyKind (image): %v", err)
	}
	bc, ok := ufImg.BoxConfig("my-image")
	if !ok {
		t.Fatalf("image shape not folded into uf.Box; boxes=%v", boxKeys(ufImg.Box))
	}
	if ufImg.Candy["my-image"] != nil {
		t.Fatal("image shape also landed in uf.Candy — must be uf.Box ONLY")
	}
	var baseBox spec.BoxConfig
	if err := decodeNodeValue(imgGn, &baseBox); err != nil {
		t.Fatalf("baseline decodeNodeValue (image): %v", err)
	}
	if got, want := mustJSON(t, bc), mustJSON(t, baseBox); got != want {
		t.Fatalf("IMAGE-shape plugin fold != direct core decode\n plugin: %s\n core:   %s", got, want)
	}

	// --- LAYER shape (no base/from → uf.Candy) ---
	layerDoc := `my-layer:
    candy:
        version: "2026.150.0000"
        description: a layer
        package:
            - git
        plan:
            - run: install a marker
              command: "true"
              run_as: root
            - check: the marker exists
              command: "true"
`
	layerGn := candyNodeFromYAML(t, layerDoc)
	var ufLayer UnifiedFile
	if err := foldCandyKind(prov, layerGn, &ufLayer); err != nil {
		t.Fatalf("foldCandyKind (layer): %v", err)
	}
	ic, ok := decodeInlineCandy(ufLayer.Candy["my-layer"])
	if !ok {
		t.Fatalf("layer shape not folded into uf.Candy; candies=%v", mapKeys(ufLayer.Candy))
	}
	if _, dup := ufLayer.Box["my-layer"]; dup {
		t.Fatal("layer shape also landed in uf.Box — must be uf.Candy ONLY")
	}
	_, baseIc, err := buildCandy(layerGn)
	if err != nil {
		t.Fatalf("baseline buildCandy (layer): %v", err)
	}
	if got, want := mustJSON(t, ic), mustJSON(t, baseIc); got != want {
		t.Fatalf("LAYER-shape plugin fold != direct core decode\n plugin: %s\n core:   %s", got, want)
	}
}
