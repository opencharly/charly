package main

// node_candy.go — the candy constructor for the unified node-form model. A candy
// node's discriminator value carries the scalars / composition refs / config maps;
// its children (package/env/service/… data nodes + run/check/… step nodes) fold
// into the candy body via the generic assembler, then decode through the shared
// CUE entity decoder. Proven byte-identical to the kind-first decode by
// TestBuildCandy_RoundTrip (node_candy_test.go).

import (
	"fmt"
	"github.com/opencharly/sdk/spec"

	"gopkg.in/yaml.v3"
)

// candyIsImage reports whether a candy: node is a full IMAGE (the former box:): it
// carries the box base⊻from marker — `base:` (an external base) or `from:` (a builder
// ref). A LAYER fragment has neither (no layer-candy uses `from:` in the corpus). This
// is the CORE box⊻layer routing predicate (alongside buildCandy): the dedicated candy
// KindProvider (plugin_candy.go) calls it in-proc to pick uf.Box vs uf.Candy, and the
// discovered-candy pre-check in unified.go calls it to distinguish a lazy LAYER ref
// from an eager IMAGE decode — so it stays in CORE, never the provider file.
func candyIsImage(gn *genericNode) bool {
	dv := gn.discValue
	if dv == nil || dv.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i+1 < len(dv.Content); i += 2 {
		switch dv.Content[i].Value {
		case "base", "from":
			return true
		}
	}
	return false
}

// buildCandy turns a `candy`-discriminator genericNode into an InlineCandy. The
// candy name is the node name (the entity-map key), returned alongside.
func buildCandy(gn *genericNode) (name string, ic *InlineCandy, err error) {
	if gn.disc != "candy" {
		return "", nil, fmt.Errorf("buildCandy: node %q is not a candy (disc %q)", gn.name, gn.disc)
	}
	// Decode-ONLY at load (fast, runs on every invocation): the full closed-schema
	// CUE validation (CalVer/enum/unknown-key checks) runs at `charly box validate`
	// (validateCandyManifestCUE), not here — matching the legacy parseCandyYAML.
	var c spec.CandyYAML
	if err := decodeNodeValue(gn, &c); err != nil {
		return "", nil, err
	}
	// Name is the node KEY in node-form (the migration moves a legacy body
	// `name:` up to the key), so stamp it — the decoded body carries no `name:`.
	c.Name = gn.name
	return gn.name, &InlineCandy{CandyYAML: c}, nil
}
