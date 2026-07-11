package main

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// node_childform_test.go — check-coverage for the COMPACT node-form grammar
// (node_parse.go + node_desugar.go): an entity is `<name>: {<kind>: <FULL BODY>,
// <member-name>: <entity>…}` — collections and the ordered `plan:` step list live
// INLINE in the kind value, member children stay named children under a
// deployable kind, and every residual old-shape data/step child is a hard load
// error pointing at `charly migrate`. The former named child-node layer was
// DELETED in the schema-compaction cutover; these tests pin the NEW grammar's
// accept/reject behavior.

// parseDocNodes parses a node-form YAML doc's top-level nodes (the loader's
// pipeline up to normalize).
func parseDocNodes(t *testing.T, nodeform string) []*genericNode {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(nodeform), &doc); err != nil {
		t.Fatalf("yaml: %v", err)
	}
	nodes, err := genericNodesFromDoc(&doc)
	if err != nil {
		t.Fatalf("parseNodeTree: %v", err)
	}
	return nodes
}

// parseDocNodesErr is the error-expecting twin of parseDocNodes.
func parseDocNodesErr(t *testing.T, nodeform string) error {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(nodeform), &doc); err != nil {
		t.Fatalf("yaml: %v", err)
	}
	_, err := genericNodesFromDoc(&doc)
	return err
}

// TestChildForm_ParseAssemble: a compact-form candy with scalars + composition +
// collections + an inline plan parses as ONE node and buildCandy folds the
// COMPLETE Candy. The plan step's `file:` sugar key is desugared at parse time
// into the internal plugin/plugin_input envelope.
func TestChildForm_ParseAssemble(t *testing.T) {
	nodeform := "redis:\n" +
		"  candy:\n" +
		"    version: \"2026.150.0000\"\n" +
		"    status: working\n" +
		"    candy: [supervisord]\n" +
		"    package: [redis, redis-cli]\n" +
		"    env: {REDIS_PORT: \"6379\"}\n" +
		"    plan:\n" +
		"      - check: binary exists\n" +
		"        file: /usr/bin/redis-server\n"
	nodes := parseDocNodes(t, nodeform)
	if len(nodes) != 1 || nodes[0].disc != "candy" {
		t.Fatalf("want one candy node, got %d (disc %q)", len(nodes), nodes[0].disc)
	}
	if len(nodes[0].children) != 0 {
		t.Fatalf("a compact candy node has no member children, got %d", len(nodes[0].children))
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
		t.Errorf("inline package collection not folded: %v", c.Package)
	}
	if len(c.Candy) != 1 {
		t.Errorf("inline composition not folded: %v", c.Candy)
	}
	if c.Env["REDIS_PORT"] != "6379" {
		t.Errorf("inline env map not folded: %v", c.Env)
	}
	if len(c.Plan) != 1 {
		t.Fatalf("inline plan step not folded: %d", len(c.Plan))
	}
	// The parse-time desugar rewrote the `file:` sugar into the internal envelope.
	op := c.Plan[0].Op
	if op.Plugin != "file" {
		t.Errorf("step sugar not desugared: plugin=%q, want file", op.Plugin)
	}
	if op.PluginInput["file"] != "/usr/bin/redis-server" {
		t.Errorf("scalar shorthand not folded onto the primary: %v", op.PluginInput)
	}
}

// TestChildForm_OldShapeDataChildRejected: a residual old-shape DATA child
// (`redis-package: {package: […]}`) under a non-deployable kind is a hard error
// pointing at `charly migrate` — collections live INLINE now.
func TestChildForm_OldShapeDataChildRejected(t *testing.T) {
	resetDeclaredPrescanRegistries()
	err := parseDocNodesErr(t, "redis:\n"+
		"  candy: {version: \"2026.150.0000\"}\n"+
		"  redis-package:\n"+
		"    package: [redis]\n")
	if err == nil || !strings.Contains(err.Error(), "is not allowed") {
		t.Fatalf("expected old-shape data-child rejection, got %v", err)
	}
	if !strings.Contains(err.Error(), "charly migrate") {
		t.Errorf("rejection must hint at `charly migrate`, got %v", err)
	}
}

// TestChildForm_OldShapeStepChildRejected: a residual old-shape STEP child
// (`shop-step-0: {check: …}`) under a DEPLOYABLE kind parses as a member child,
// but the step mapping carries no kind discriminator — rejected with the
// migrate hint (steps live in the inline `plan:` list now).
func TestChildForm_OldShapeStepChildRejected(t *testing.T) {
	resetDeclaredPrescanRegistries()
	err := parseDocNodesErr(t, "shop:\n"+
		"  group: {}\n"+
		"  shop-step-0:\n"+
		"    check: reaches the cache\n"+
		"    command: redis-cli ping\n")
	if err == nil || !strings.Contains(err.Error(), "no kind discriminator") {
		t.Fatalf("expected old-shape step-child rejection, got %v", err)
	}
	if !strings.Contains(err.Error(), "charly migrate") {
		t.Errorf("rejection must hint at `charly migrate`, got %v", err)
	}
}

// TestChildForm_TwoKindKeysRejected: an entity carries exactly ONE kind
// discriminator — a second kind key (equally: a member child NAMED like a kind
// word) is a hard parse error.
func TestChildForm_TwoKindKeysRejected(t *testing.T) {
	resetDeclaredPrescanRegistries()
	err := parseDocNodesErr(t, "weird:\n"+
		"  candy: {version: \"2026.150.0000\"}\n"+
		"  pod: {image: coder}\n")
	if err == nil || !strings.Contains(err.Error(), "two kind discriminators") {
		t.Fatalf("expected two-kind-discriminators rejection, got %v", err)
	}
}

// TestChildForm_WrongKindChild: a non-deployable kind (candy) may NOT nest a
// sub-entity member (a pod) — only deployable kinds (#ResourceKind) or an
// external structural plugin kind do.
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
	doc := "redis:\n  candy: {version: \"2026.150.0000\"}\n  inner:\n    pod: {image: x}\n"
	err := parseDocNodesErr(t, doc)
	if err == nil || !strings.Contains(err.Error(), "is not allowed") {
		t.Fatalf("expected wrong-kind-child rejection, got %v", err)
	}
	if !strings.Contains(err.Error(), "charly migrate") {
		t.Errorf("rejection must hint at `charly migrate`, got %v", err)
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
