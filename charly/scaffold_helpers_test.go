package main

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestAppendCandyPackages_UnderCandyWrapper guards that add-<fmt> writes packages
// INSIDE `candy:` under the canonical `distro:` map (add-rpm → distro.fedora.package),
// never as a stray top-level key the loader would now reject.
func TestAppendCandyPackages_UnderCandyWrapper(t *testing.T) {
	dir := t.TempDir()
	candyDir := filepath.Join(dir, "candy", "foo")
	if err := os.MkdirAll(candyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(candyDir, UnifiedFileName),
		[]byte("candy:\n  name: foo\n  version: 2026.001.0001\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	if err := appendCandyPackages("foo", "rpm", []string{"ripgrep", "ripgrep"}); err != nil {
		t.Fatalf("appendCandyPackages: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(candyDir, UnifiedFileName))
	var root map[string]any
	if err := yaml.Unmarshal(data, &root); err != nil {
		t.Fatalf("re-parse: %v\n%s", err, data)
	}
	if _, stray := root["rpm"]; stray {
		t.Fatalf("stray top-level rpm: introduced\n%s", data)
	}
	if _, stray := root["distro"]; stray {
		t.Fatalf("stray top-level distro: introduced (must be under candy:)\n%s", data)
	}
	candy := root["candy"].(map[string]any)
	distro, ok := candy["distro"].(map[string]any)
	if !ok {
		t.Fatalf("candy.distro missing\n%s", data)
	}
	fedora, ok := distro["fedora"].(map[string]any)
	if !ok {
		t.Fatalf("candy.distro.fedora missing (add-rpm → distro.fedora)\n%s", data)
	}
	pkgs := fedora["package"].([]any)
	if len(pkgs) != 1 || pkgs[0] != "ripgrep" { // deduped
		t.Fatalf("want distro.fedora.package=[ripgrep] (deduped), got %v", pkgs)
	}
}
