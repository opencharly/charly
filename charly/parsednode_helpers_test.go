package main

import (
	"testing"

	"github.com/opencharly/spec/spec"
	"gopkg.in/yaml.v3"
)

// parsedNodesFromDoc parses a node-form document into []spec.ParsedNode via the PRODUCTION
// loader (the registered config front-end sdk/loaderkit) — the direct parsed-node fixture
// builder for every node-form test (parser consolidation F2.1: the former genericNodesFromDoc
// bridge helper + its genericToParsedNode round trip are deleted; a test that needs the parsed
// node builds it here, never through a genericNode reconstruction).
func parsedNodesFromDoc(t *testing.T, doc *yaml.Node) []spec.ParsedNode {
	t.Helper()
	_, pp, err := activeLoaderParser.ParseDoc(doc, loaderThreaded())
	if err != nil {
		t.Fatalf("parsedNodesFromDoc: %v", err)
	}
	return pp.Nodes
}

// singleParsedNode parses a ONE-entity node-form doc and returns its single parsed node.
func singleParsedNode(t *testing.T, doc string) spec.ParsedNode {
	t.Helper()
	var ydoc yaml.Node
	if err := yaml.Unmarshal([]byte(doc), &ydoc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	nodes := parsedNodesFromDoc(t, &ydoc)
	if len(nodes) != 1 {
		t.Fatalf("want 1 node, got %d", len(nodes))
	}
	return nodes[0]
}

// resetDeclaredPrescanRegistries clears the process-global prescan registries so a test that
// READS them via the wrong-kind-child gate (externalKindMayNestMembers → isDeclaredExternalKind)
// starts from a clean slate, isolated from a prior test's LoadUnified that left a kind declared.
// Production never accumulates — each `charly` process (and each `charly mcp serve` tool-call
// fork) loads exactly one project into a fresh process — so this isolation is a test-only concern.
// (Relocated here from the deleted bridge test file node_childform_test.go, parser
// consolidation F2.1.)
func resetDeclaredPrescanRegistries() {
	declaredDeployMu.Lock()
	declaredDeploySubstrate = map[string]bool{}
	declaredExternalCommand = map[string]bool{}
	declaredKind = map[string]bool{}
	declaredDeployMu.Unlock()
}
