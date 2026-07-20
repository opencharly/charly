package authoring

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/opencharly/sdk/kit"
)

// authoring_edit_test.go — the addCandyToBox / removeCandyFromBox edit-helper tests, moved from
// charly/scaffold_project_test.go when the box authoring command family externalized to this
// plugin (P14b). The helpers walk *yaml.Node trees via kit.MappingChild / kit.SaveYAMLNodeFile
// (comment-preserving), so these cover the idempotent-append, remove (incl. no-op + missing-box
// error), and the flat-imported-per-kind-file resolution paths.

// TestAddCandyToBox covers the idempotent-append behaviour.
func TestAddCandyToBox(t *testing.T) {
	dir := t.TempDir()
	if err := kit.ScaffoldProject(dir); err != nil {
		t.Fatalf("ScaffoldProject: %v", err)
	}
	if err := kit.AddBox(dir, "hello", "fedora", nil); err != nil {
		t.Fatalf("AddBox: %v", err)
	}
	if err := addCandyToBox(dir, "hello", "sshd"); err != nil {
		t.Fatalf("addCandyToBox first: %v", err)
	}
	if err := addCandyToBox(dir, "hello", "sshd"); err != nil {
		t.Fatalf("addCandyToBox second (idempotent): %v", err)
	}
	if err := addCandyToBox(dir, "hello", "tmux"); err != nil {
		t.Fatalf("addCandyToBox tmux: %v", err)
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

// TestRemoveCandyFromBox covers the remove path including the no-op when the candy isn't present.
func TestRemoveCandyFromBox(t *testing.T) {
	dir := t.TempDir()
	if err := kit.ScaffoldProject(dir); err != nil {
		t.Fatalf("ScaffoldProject: %v", err)
	}
	if err := kit.AddBox(dir, "hello", "fedora", []string{"sshd", "tmux"}); err != nil {
		t.Fatalf("AddBox: %v", err)
	}
	if err := removeCandyFromBox(dir, "hello", "sshd"); err != nil {
		t.Fatalf("removeCandyFromBox: %v", err)
	}
	// No-op for missing candy.
	if err := removeCandyFromBox(dir, "hello", "not-there"); err != nil {
		t.Fatalf("removeCandyFromBox no-op: %v", err)
	}
	// Error path: missing box.
	if err := removeCandyFromBox(dir, "ghost", "sshd"); err == nil {
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

// TestEditCandy_ImportedBoxFile verifies the authoring-edit verbs resolve a box defined in a
// flat-imported per-kind file (box.yml) and save the edit THERE — instead of erroring "box not
// found in charly.yml". This is the fix for `charly box rm-candy <leaf> charly` / `charly box
// add-candy` on boxes that live in box.yml rather than inlined in charly.yml.
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

	if err := removeCandyFromBox(dir, "leafy", "charly"); err != nil {
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

	if err := addCandyToBox(dir, "leafy", "ripgrep"); err != nil {
		t.Fatalf("add-candy on imported box.yml entry: %v", err)
	}
	if got := candy(); got != "supervisord,jupyter,ripgrep" {
		t.Errorf("after add-candy ripgrep: candy = %q", got)
	}

	if err := removeCandyFromBox(dir, "nonexistent", "charly"); err == nil {
		t.Error("expected error for a genuinely-missing image")
	}
}

// TestResolveProjectFile covers the path-traversal guard for `charly box write` / `box cat` —
// the one safety boundary for the free-form file escape hatch.
func TestResolveProjectFile(t *testing.T) {
	dir := t.TempDir()

	if _, err := resolveProjectFile(dir, ""); err == nil {
		t.Error("expected error for empty path")
	}
	if _, err := resolveProjectFile(dir, "/etc/passwd"); err == nil {
		t.Error("expected error for absolute path")
	}
	if _, err := resolveProjectFile(dir, "../escape.txt"); err == nil {
		t.Error("expected error for parent-traversal path")
	}
	if _, err := resolveProjectFile(dir, "sub/../../../escape.txt"); err == nil {
		t.Error("expected error for nested parent-traversal path")
	}
	// A clean relative path resolves under the project root.
	got, err := resolveProjectFile(dir, "candy/mycandy/charly.yml")
	if err != nil {
		t.Fatalf("unexpected error for clean relative path: %v", err)
	}
	want := filepath.Join(dir, "candy", "mycandy", "charly.yml")
	if got != want {
		t.Errorf("resolveProjectFile = %q; want %q", got, want)
	}
}
