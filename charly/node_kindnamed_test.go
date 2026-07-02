package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestNodeForm_KindNamedEntity is the regression test for an entity NAMED after a
// reserved kind keyword (the real case: a box named `k8s`). The migration flattens
// `box: {k8s: …}` to the name-first `k8s: {candy: …}` (EDGE-INHERIT cutover D: an
// image is a `candy:` node carrying `base:`), where the top-level key `k8s`
// collides with the `k8s` kind keyword. Two loader/validate sites must handle it:
//
//   - applyDiscoveredManifest routes every discovered manifest via classifyDoc,
//     which inspects the VALUE shape (nodeShapedValue: a `<kind>` discriminator)
//     and reports docShapeNode — so the box named `k8s` is parsed as a node-form
//     image (a candy: node with base:), not mis-decoded as a k8s-kind entity.
//   - validateVocabularyCollections (the root-shape collection validator) would
//     read top-level `k8s:` as the k8s collection and validate the `candy` child
//     against #K8s; it skips node-form files (isNodeFormFile).
//
// Without either fix this test FAILS (load error / validation error).
func TestNodeForm_KindNamedEntity(t *testing.T) {
	dir := t.TempDir()
	must := func(p, body string) {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	must(filepath.Join(dir, "charly.yml"), `version: "`+latestSchemaVersion.String()+`"
discover:
  - box
`)
	// A box NAMED after the `k8s` kind keyword, in node-form (an image is a
	// `candy:` node carrying `base:` — EDGE-INHERIT cutover D).
	must(filepath.Join(dir, "box", "k8s", "charly.yml"), "k8s:\n  candy:\n    base: fedora\n")

	uf, _, err := LoadUnified(dir)
	if err != nil {
		t.Fatalf("LoadUnified rejected a box named after a kind keyword: %v", err)
	}
	b, ok := uf.Box["k8s"]
	if !ok {
		t.Fatalf("box named 'k8s' not loaded as a box; boxes=%v", boxKeys(uf.Box))
	}
	if b.Base != "fedora" {
		t.Errorf("k8s box base = %q, want fedora (misdecoded as a k8s-kind entity?)", b.Base)
	}

	// The legacy root-shape collection validator must SKIP this node-form file
	// (classifyDoc → docShapeNode), or it validates the `box` child against #K8s.
	data, _ := os.ReadFile(filepath.Join(dir, "box", "k8s", "charly.yml"))
	if !isNodeFormFile(data) {
		t.Error("kind-named node-form file not recognized as node-form — the legacy collection validator would misvalidate it")
	}
}
