package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestScaffoldProject covers the happy path + the don't-clobber guard.
// Doesn't run `box validate`, that's exercised in TestScaffoldProject_AddImageRoundtrip.
func TestScaffoldProject(t *testing.T) {
	dir := t.TempDir()
	if err := ScaffoldProject(dir); err != nil {
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
	if err := ScaffoldProject(dir); err == nil {
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
	if err := ScaffoldProject(dir); err != nil {
		t.Fatalf("ScaffoldProject: %v", err)
	}
	if err := AddBox(dir, "hello", "quay.io/fedora/fedora:43", []string{"sshd"}); err != nil {
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

// TestAddCandyToImage covers the idempotent-append behaviour.
func TestAddCandyToImage(t *testing.T) {
	dir := t.TempDir()
	if err := ScaffoldProject(dir); err != nil {
		t.Fatalf("ScaffoldProject: %v", err)
	}
	if err := AddBox(dir, "hello", "fedora", nil); err != nil {
		t.Fatalf("AddBox: %v", err)
	}
	if err := AddCandyToBox(dir, "hello", "sshd"); err != nil {
		t.Fatalf("AddCandyToBox first: %v", err)
	}
	if err := AddCandyToBox(dir, "hello", "sshd"); err != nil {
		t.Fatalf("AddCandyToBox second (idempotent): %v", err)
	}
	if err := AddCandyToBox(dir, "hello", "tmux"); err != nil {
		t.Fatalf("AddCandyToBox tmux: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "box", "hello", "charly.yml"))
	// sshd should appear exactly once, tmux exactly once.
	if got := strings.Count(string(data), "- sshd"); got != 1 {
		t.Errorf("sshd appears %d times; want 1\n%s", got, data)
	}
	if got := strings.Count(string(data), "- tmux"); got != 1 {
		t.Errorf("tmux appears %d times; want 1\n%s", got, data)
	}
}

// TestRemoveCandyFromImage covers the remove path including the no-op when
// the candy isn't present.
func TestRemoveCandyFromImage(t *testing.T) {
	dir := t.TempDir()
	if err := ScaffoldProject(dir); err != nil {
		t.Fatalf("ScaffoldProject: %v", err)
	}
	if err := AddBox(dir, "hello", "fedora", []string{"sshd", "tmux"}); err != nil {
		t.Fatalf("AddBox: %v", err)
	}
	if err := RemoveCandyFromBox(dir, "hello", "sshd"); err != nil {
		t.Fatalf("RemoveCandyFromBox: %v", err)
	}
	// No-op for missing candy.
	if err := RemoveCandyFromBox(dir, "hello", "not-there"); err != nil {
		t.Fatalf("RemoveCandyFromBox no-op: %v", err)
	}
	// Error path: missing box.
	if err := RemoveCandyFromBox(dir, "ghost", "sshd"); err == nil {
		t.Errorf("expected error for missing image; got nil")
	}
	data, _ := os.ReadFile(filepath.Join(dir, "box", "hello", "charly.yml"))
	if strings.Contains(string(data), "sshd") {
		t.Errorf("sshd should be removed; box/hello/charly.yml=\n%s", data)
	}
	if !strings.Contains(string(data), "tmux") {
		t.Errorf("tmux should remain; box/hello/charly.yml=\n%s", data)
	}
}

// TestEditCandy_ImportedBoxFile verifies the authoring-edit verbs resolve a
// box defined in a flat-imported per-kind file (box.yml) and save the edit
// THERE — instead of erroring "box not found in charly.yml". This is the
// fix for `charly box rm-candy <leaf> charly` / `charly box add-candy` on boxes that live
// in box.yml rather than inlined in charly.yml.
func TestEditCandy_ImportedBoxFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "charly.yml"),
		[]byte("version: 2026.156.0001\nimport:\n    - box.yml\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	boxPath := filepath.Join(dir, "box.yml")
	// Node-form IMAGE (EDGE-INHERIT cutover D): `<name>: {candy: {base, candy: …}}`.
	if err := os.WriteFile(boxPath,
		[]byte("leafy:\n    candy:\n        base: fedora\n        candy:\n            - supervisord\n            - charly\n            - jupyter\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	candy := func() string {
		data, _ := os.ReadFile(boxPath)
		var m map[string]struct {
			Candy struct {
				Candy []string `yaml:"candy"`
			} `yaml:"candy"`
		}
		_ = yaml.Unmarshal(data, &m)
		return strings.Join(m["leafy"].Candy.Candy, ",")
	}

	if err := RemoveCandyFromBox(dir, "leafy", "charly"); err != nil {
		t.Fatalf("rm-candy on imported box.yml entry: %v", err)
	}
	if got := candy(); got != "supervisord,jupyter" {
		t.Errorf("after rm-candy charly: candy = %q, want supervisord,jupyter", got)
	}
	// The edit must land in box.yml, NOT leak into charly.yml.
	charlyData, _ := os.ReadFile(filepath.Join(dir, "charly.yml"))
	if strings.Contains(string(charlyData), "leafy") {
		t.Errorf("edit leaked into charly.yml:\n%s", charlyData)
	}

	if err := AddCandyToBox(dir, "leafy", "ripgrep"); err != nil {
		t.Fatalf("add-candy on imported box.yml entry: %v", err)
	}
	if got := candy(); got != "supervisord,jupyter,ripgrep" {
		t.Errorf("after add-candy ripgrep: candy = %q", got)
	}

	if err := RemoveCandyFromBox(dir, "nonexistent", "charly"); err == nil {
		t.Error("expected error for a genuinely-missing image")
	}
}
