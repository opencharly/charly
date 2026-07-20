package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/opencharly/sdk/kit"
)

// TestScaffoldProject covers the happy path + the don't-clobber guard.
// Doesn't run `box validate`, that's exercised in TestScaffoldProject_AddImageRoundtrip.
func TestScaffoldProject(t *testing.T) {
	dir := t.TempDir()
	if err := kit.ScaffoldProject(dir); err != nil {
		t.Fatalf("ScaffoldProject: %v", err)
	}
	for _, p := range []string{"charly.yml", "candy", ".gitignore"} {
		if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
			t.Errorf("expected %s to exist: %v", p, err)
		}
	}
	// The scaffolder must NEVER write a per-kind box.yml — schema v4
	// canonical authoring target is charly.yml only.
	if _, err := os.Stat(filepath.Join(dir, "box.yml")); err == nil {
		t.Errorf("expected NO per-kind box.yml at scaffold root (schema v4); found one")
	}
	// Idempotency: re-scaffolding the same dir should fail (we never
	// silently clobber an existing charly.yml).
	if err := kit.ScaffoldProject(dir); err == nil {
		t.Errorf("expected re-scaffold to error; got nil")
	}
}

// TestScaffoldProject_AddImageRoundtrip is the integration test the plan
// names. Scaffold a project, add a box, round-trip through the parser,
// and confirm both the box appears AND the leading comment block at
// top of charly.yml is preserved (proves the yaml.Node API is wired
// correctly and not destroying authoring metadata).
func TestScaffoldProject_AddImageRoundtrip(t *testing.T) {
	dir := t.TempDir()
	if err := kit.ScaffoldProject(dir); err != nil {
		t.Fatalf("ScaffoldProject: %v", err)
	}
	if err := kit.AddBox(dir, "hello", "quay.io/fedora/fedora:43", []string{"sshd"}); err != nil {
		t.Fatalf("AddBox: %v", err)
	}
	// The scaffold's charly.yml leading comment is untouched — AddBox writes a
	// separate discovered per-box file box/hello/charly.yml.
	rootData, err := os.ReadFile(filepath.Join(dir, "charly.yml"))
	if err != nil {
		t.Fatalf("read charly.yml: %v", err)
	}
	if !strings.Contains(string(rootData), "unified project root") {
		t.Errorf("scaffold's leading comment was destroyed; charly.yml=\n%s", rootData)
	}
	// AddBox writes box/hello/charly.yml as a node-form IMAGE: the box NAME is the
	// top-level key and `candy:` is the image discriminator (EDGE-INHERIT cutover
	// D — an image is a `candy:` node carrying `base:`).
	data, err := os.ReadFile(filepath.Join(dir, "box", "hello", "charly.yml"))
	if err != nil {
		t.Fatalf("read box/hello/charly.yml: %v", err)
	}
	var doc map[string]struct {
		Candy struct {
			Base    string   `yaml:"base"`
			Candies []string `yaml:"candy"`
		} `yaml:"candy"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("re-parse: %v\n%s", err, data)
	}
	img, ok := doc["hello"]
	if !ok {
		t.Fatalf("no top-level node named hello\n%s", data)
	}
	if img.Candy.Base != "quay.io/fedora/fedora:43" {
		t.Errorf("base = %q; want quay.io/fedora/fedora:43", img.Candy.Base)
	}
	if len(img.Candy.Candies) != 1 || img.Candy.Candies[0] != "sshd" {
		t.Errorf("candy = %v; want [sshd]", img.Candy.Candies)
	}
}

// The AddCandyToBox / RemoveCandyFromBox edit-helper tests
// (TestAddCandyToImage / TestRemoveCandyFromImage / TestEditCandy_ImportedBoxFile) moved WITH the
// helpers to candy/plugin-authoring (authoring_edit_test.go) when the box authoring command family
// externalized (P14b). The two tests below cover kit.ScaffoldProject / kit.AddBox (the create-side
// ENGINE that stays in sdk/kit, shared with candy/plugin-box's command:new) — they exercise the
// kit scaffolder from core and remain here.
