package main

import (
	"encoding/json"
	"fmt"

	"github.com/opencharly/spec/spec"
	"gopkg.in/yaml.v3"
)

// node_parsed.go — the genericNode BRIDGE, and nothing else.
//
// The PARSE (yaml → spec.ParsedProject) runs in the loader plugin via sdk/loaderkit, and since K1
// unit 3b the TRUE clause-M dispatch (provider_kind_invoke.go) threads spec.ParsedNode straight
// through its own call chain — it no longer reconstructs genericNode for its main flow. What
// survives here is the LOCAL, small-footprint conversion between the wire-safe spec.ParsedNode and
// the *genericNode that a handful of calls genuinely still need: the bootstrap-critical
// candyIsImage/buildCandy routing (clause B — provider_kind_invoke.go's foldCandyKind,
// materialize.go's materializeDiscoveredNode, layers.go's candy-manifest parse) and
// validateKindValueCUE's raw discValue shape check.
//
// The MATERIALIZE plumbing that used to share this file (materializeNodeInto's accumulator
// seed/copy-back and materializeProject's per-document loop) colocated onto materialize.go in
// K-wave 2 cone R1 unit C — that file holds every other host-coupled materialize leg plus the
// MaterializeProjectSeams those two are reached through, so it is their owner. Only
// normalizeNodeInto still crosses over, and it is the test-facing bridge entry, which is exactly
// what this file is for.

// parsedNodeToGeneric reconstructs the genericNode a bootstrap-critical clause-B call
// (candyIsImage/buildCandy) or the raw-discValue-shape check (validateKindValueCUE,
// provider_kind_invoke.go) needs, from a spec.ParsedNode: the JSON body becomes the discValue
// mapping node those calls read directly. Pure (no registry/host coupling) — safe to call
// wherever a genericNode is genuinely needed, never a re-entrant load.
func parsedNodeToGeneric(pn spec.ParsedNode) (*genericNode, error) {
	gn := &genericNode{name: pn.Name, disc: pn.Disc, discClass: "entity"}
	if len(pn.Body) > 0 {
		var asAny any
		if err := json.Unmarshal([]byte(pn.Body), &asAny); err != nil {
			return nil, fmt.Errorf("node %q: decode body: %w", pn.Name, err)
		}
		var dv yaml.Node
		if err := dv.Encode(asAny); err != nil {
			return nil, fmt.Errorf("node %q: encode body: %w", pn.Name, err)
		}
		gn.discValue = &dv
	}
	for _, ch := range pn.Children {
		cgn, err := parsedNodeToGeneric(*ch)
		if err != nil {
			return nil, err
		}
		gn.children = append(gn.children, cgn)
	}
	return gn, nil
}

// genericToParsedNode is the INVERSE of parsedNodeToGeneric: it rebuilds the wire-safe
// spec.ParsedNode a *genericNode represents. Used in production by node_build.go's decodeNodeValue
// (the ONE genericNode-typed core wrapper still standing, for node_candy.go's bootstrap-critical
// buildCandy) to reach the relocated entity-body-assembly + CUE-decode mechanism (sdk/loaderkit, K1
// unit 3b) — genericNode never crosses the ProjectLoader seam itself, only its wire-safe
// spec.ParsedNode form does. Also exercised by tests that build a *genericNode fixture by hand
// (rather than parsing real YAML) to drive the K1 unit-1 Materializer pipeline via
// normalizeNodeInto below.
func genericToParsedNode(gn *genericNode) (spec.ParsedNode, error) {
	pn := spec.ParsedNode{Name: gn.name, Disc: gn.disc}
	if gn.discValue != nil {
		var asAny any
		if err := gn.discValue.Decode(&asAny); err != nil {
			return spec.ParsedNode{}, fmt.Errorf("node %q: decode value: %w", gn.name, err)
		}
		b, err := json.Marshal(asAny)
		if err != nil {
			return spec.ParsedNode{}, fmt.Errorf("node %q: marshal value: %w", gn.name, err)
		}
		pn.Body = json.RawMessage(b)
	}
	for _, ch := range gn.children {
		cpn, err := genericToParsedNode(ch)
		if err != nil {
			return spec.ParsedNode{}, err
		}
		pn.Children = append(pn.Children, &cpn)
	}
	return pn, nil
}

// normalizeNodeInto is the TEST-FACING bridge into the K1 unit-1 Materializer pipeline: it
// converts gn back to a spec.ParsedNode (genericToParsedNode) and runs materializeNodeInto —
// preserving the pre-K1 call shape (*genericNode, *spec.UnifiedFile) for tests that build a genericNode
// fixture directly. Production load paths (materializeProject / materializeDiscoveredNode) never
// call this; they already hold a genuine spec.ParsedNode from the registered DocParser/
// ProjectWalker and call materializeNodeInto directly.
func normalizeNodeInto(gn *genericNode, uf *spec.UnifiedFile) error {
	pn, err := genericToParsedNode(gn)
	if err != nil {
		return err
	}
	return materializeNodeInto(pn, uf)
}
