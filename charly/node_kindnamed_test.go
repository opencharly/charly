package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/opencharly/spec/spec"
	"gopkg.in/yaml.v3"
)

// TestNodeForm_KindNamedEntity is the regression test for an entity NAMED after a
// reserved kind keyword (the real case: a box named `k8s`). The migration flattens
// `box: {k8s: …}` to the name-first `k8s: {candy: …}` (EDGE-INHERIT cutover D: an
// image is a `candy:` node carrying `base:`), where the top-level key `k8s`
// collides with the `k8s` kind keyword. Two loader/validate sites must handle it:
//
//   - ApplyDiscover's walk (loaderkit.RunDiscover, parsing each discovered manifest
//     via kit.ClassifyDoc) inspects the VALUE shape (kit.NodeShapedValue: a `<kind>`
//     discriminator) and reports kit.DocShapeNode — so the box named `k8s` is parsed
//     as a node-form image (a candy: node with base:), not mis-decoded as a k8s-kind
//     entity.
//   - the document CLASSIFIER must independently report node-form for the same
//     file, because every validate path that reads a root manifest gates on that
//     classification: `charly box validate`'s step-typo pass (candy/plugin-box's
//     validateProjectCUESchemas → isNodeFormFile) runs the closed #Step/#Op gate
//     ONLY on a node-form file, and a file misread as root-shape is hard-rejected
//     at load with a `charly migrate` hint. Misread, top-level `k8s:` looks like
//     the k8s collection and its `candy` child gets validated against #K8s.
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
	b, ok := uf.BoxConfig("k8s")
	if !ok {
		t.Fatalf("box named 'k8s' not loaded as a box; boxes=%v", boxKeys(uf.Box))
	}
	if b.Base != "fedora" {
		t.Errorf("k8s box base = %q, want fedora (misdecoded as a k8s-kind entity?)", b.Base)
	}

	// The classifier must report node-form for this file — the same spec.ClassifyDoc verdict
	// candy/plugin-box's isNodeFormFile gates the step-typo pass on. Asserted directly (rather than
	// through that plugin helper, which lives in a different module) so this stays a pure
	// classifier regression test.
	data, _ := os.ReadFile(filepath.Join(dir, "box", "k8s", "charly.yml"))
	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		t.Fatalf("kind-named manifest did not parse as YAML: %v", err)
	}
	shape, cerr := spec.ClassifyDoc(&node)
	if cerr != nil || shape != spec.DocShapeNode {
		t.Errorf("kind-named file classified as %v (err %v), want DocShapeNode — a validate path would misvalidate it", shape, cerr)
	}
}
