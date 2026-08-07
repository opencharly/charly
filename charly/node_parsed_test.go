package main

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/opencharly/spec/spec"
	"gopkg.in/yaml.v3"
)

// genericNodesFromDoc parses a node-form document into genericNodes via the PRODUCTION loader
// (the registered config front-end sdk/loaderkit) + the materialize reconstruction — the SAME
// path production uses (P6). The test-side helper that runs the production loader parse, so
// the genericNode-consuming tests (candy/substrate/fleet decode) exercise the real reconstructed
// node, not a separate decomposition.
func genericNodesFromDoc(doc *yaml.Node) ([]*genericNode, error) {
	_, pp, err := activeLoaderParser.ParseDoc(doc, loaderThreaded())
	if err != nil {
		return nil, err
	}
	out := make([]*genericNode, 0, len(pp.Nodes))
	for i := range pp.Nodes {
		gn, gerr := parsedNodeToGeneric(pp.Nodes[i])
		if gerr != nil {
			return nil, gerr
		}
		out = append(out, gn)
	}
	return out, nil
}

// TestParsedNodeToGeneric_BodyRoundTrips locks the host MATERIALIZE converter: a spec.ParsedNode
// (the loader plugin's parse output) reconstructs into the genericNode a bootstrap-critical
// clause-B call (candyIsImage/buildCandy) or validateKindValueCUE's raw-discValue-shape check
// reads (K1 unit 3b — genericNode no longer crosses the ProjectLoader seam itself), and
// genericToParsedNode's INVERSE conversion of that reconstruction returns the SAME body DATA — so
// the pn→gn→pn round trip is lossless regardless of how many times a call needs to bridge the two
// representations. Also exercises member-child recursion.
func TestParsedNodeToGeneric_BodyRoundTrips(t *testing.T) {
	pn := spec.ParsedNode{
		Name: "web",
		Disc: "pod",
		Body: []byte(`{"from":"img","port":[8080]}`),
		Children: []*spec.ParsedNode{{
			Name: "db",
			Disc: "pod",
			Body: []byte(`{"from":"dbimg"}`),
		}},
	}
	gn, err := parsedNodeToGeneric(pn)
	if err != nil {
		t.Fatalf("parsedNodeToGeneric: %v", err)
	}
	if gn.name != "web" || gn.disc != "pod" {
		t.Errorf("gn = %q/%q, want web/pod", gn.name, gn.disc)
	}
	roundTripped, err := genericToParsedNode(gn)
	if err != nil {
		t.Fatalf("genericToParsedNode: %v", err)
	}
	if !jsonEqual(t, []byte(roundTripped.Body), []byte(pn.Body)) {
		t.Fatalf("reconstructed body differs:\n  got  %s\n  want %s", roundTripped.Body, pn.Body)
	}
	if len(gn.children) != 1 || gn.children[0].name != "db" {
		t.Fatalf("children = %+v, want one 'db'", gn.children)
	}
}

// jsonEqual compares two JSON blobs by decoded value (order/formatting-insensitive).
func jsonEqual(t *testing.T, a, b []byte) bool {
	t.Helper()
	var av, bv any
	if err := json.Unmarshal(a, &av); err != nil {
		t.Fatalf("json a: %v", err)
	}
	if err := json.Unmarshal(b, &bv); err != nil {
		t.Fatalf("json b: %v", err)
	}
	return reflect.DeepEqual(av, bv)
}
