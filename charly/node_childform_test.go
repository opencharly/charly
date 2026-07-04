package main

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// node_childform_test.go — check-coverage for the unified "everything is a node"
// child-node model: an entity is `<name>: {<kind>: <scalars>, <child-nodes>}`
// (every non-scalar field + plan step a child), and the parser + assembler
// reconstruct the complete entity. Each test FAILS without the corresponding
// child-node code path.

// parseDocNodes parses a node-form YAML doc's top-level nodes (the loader's
// pipeline up to normalize).
func parseDocNodes(t *testing.T, nodeform string) []*genericNode {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(nodeform), &doc); err != nil {
		t.Fatalf("yaml: %v", err)
	}
	_, nodes, err := parseNodeTree(&doc)
	if err != nil {
		t.Fatalf("parseNodeTree: %v", err)
	}
	return nodes
}

// TestChildForm_ParseAssemble: a node-form candy with scalars + composition +
// collections + a plan-step child parses and the assembler folds every child node
// back into the COMPLETE Candy (scalars in the value, everything else reconstructed).
func TestChildForm_ParseAssemble(t *testing.T) {
	nodeform := "redis:\n" +
		"  candy:\n    version: \"2026.150.0000\"\n    status: working\n" +
		"  redis-candy:\n    candy: [supervisord]\n" +
		"  redis-package:\n    package: [redis, redis-cli]\n" +
		"  redis-env:\n    env: {REDIS_PORT: \"6379\"}\n" +
		"  redis-check:\n    check: binary exists\n    file: /usr/bin/redis-server\n"
	nodes := parseDocNodes(t, nodeform)
	if len(nodes) != 1 || nodes[0].disc != "candy" {
		t.Fatalf("want one candy node, got %d (disc %q)", len(nodes), nodes[0].disc)
	}
	_, ic, err := buildCandy(nodes[0])
	if err != nil {
		t.Fatalf("buildCandy: %v", err)
	}
	c := ic.CandyYAML
	if c.Version != "2026.150.0000" || c.Status != "working" {
		t.Errorf("scalars lost: version=%q status=%q", c.Version, c.Status)
	}
	if len(c.Package) != 2 {
		t.Errorf("package collection not folded: %v", c.Package)
	}
	if len(c.Candy) != 1 {
		t.Errorf("composition not folded: %v", c.Candy)
	}
	if c.Env["REDIS_PORT"] != "6379" {
		t.Errorf("env map not folded: %v", c.Env)
	}
	if len(c.Plan) != 1 {
		t.Errorf("plan step not folded: %d", len(c.Plan))
	}
}

// TestChildForm_WrongKindChild: a non-deployable kind (candy) may NOT nest a
// sub-entity (a pod) — only deployable kinds do. The Go gate rejects it (the CUE
// document gate accepts children as `_` for performance).
// resetDeclaredPrescanRegistries clears the process-global prescan registries so a test that
// READS them via the wrong-kind-child gate (externalKindMayNestMembers → isDeclaredExternalKind)
// starts from a clean slate, isolated from a prior test's LoadUnified that left a kind declared.
// Production never accumulates — each `charly` process (and each `charly mcp serve` tool-call
// fork) loads exactly one project into a fresh process — so this isolation is a test-only concern.
func resetDeclaredPrescanRegistries() {
	declaredDeployMu.Lock()
	declaredDeploySubstrate = map[string]bool{}
	declaredExternalCommand = map[string]bool{}
	declaredKind = map[string]bool{}
	declaredDeployMu.Unlock()
}

func TestChildForm_WrongKindChild(t *testing.T) {
	resetDeclaredPrescanRegistries()
	doc := "redis:\n  candy: {version: \"2026.150.0000\"}\n  inner:\n    pod: {box: x}\n"
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(doc), &n); err != nil {
		t.Fatal(err)
	}
	_, _, err := parseNodeTree(&n)
	if err == nil || !strings.Contains(err.Error(), "sub-entity child") {
		t.Fatalf("expected wrong-kind-child rejection, got %v", err)
	}
}

// TestChildForm_StepVerbPrecedence: a check step carrying a `port:` Op field (also a
// data-key name) is ONE step node, not two discriminators.
func TestChildForm_StepVerbPrecedence(t *testing.T) {
	gn, err := parseNode("probe", mustYAMLNode(t, "check: port 6443 listening\nport: 6443\n"), true)
	if err != nil {
		t.Fatalf("parseNode: %v", err)
	}
	if gn.discClass != "step" || gn.disc != "check" {
		t.Errorf("want step/check, got %s/%s", gn.discClass, gn.disc)
	}
}

// TestChildForm_CompositionChildIsData: a `{candy: [refs]}` child classifies as DATA
// (composition), not a sub-entity — `candy` is both a kind keyword and a data field,
// and as a child the data sense wins.
func TestChildForm_CompositionChildIsData(t *testing.T) {
	gn, err := parseNode("comp", mustYAMLNode(t, "candy: [supervisord, redis]\n"), true)
	if err != nil {
		t.Fatalf("parseNode: %v", err)
	}
	if gn.discClass != "data" || gn.disc != "candy" {
		t.Errorf("want data/candy, got %s/%s", gn.discClass, gn.disc)
	}
}

func mustYAMLNode(t *testing.T, s string) *yaml.Node {
	t.Helper()
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(s), &n); err != nil {
		t.Fatal(err)
	}
	return n.Content[0]
}
