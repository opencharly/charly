package candy

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/opencharly/sdk/kit"
)

// TestAppendCandyPackages_UnderCandyWrapper guards that add-<fmt> writes packages INSIDE the
// entity's `candy:` body under the canonical `distro:` map (add-rpm → distro.fedora.package),
// never as a stray top-level key the loader would reject — and dedupes.
func TestAppendCandyPackages_UnderCandyWrapper(t *testing.T) {
	dir := t.TempDir()
	candyDir := filepath.Join(dir, kit.DefaultCandyDir, "foo")
	if err := os.MkdirAll(candyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(candyDir, kit.UnifiedFileName),
		[]byte("foo:\n    candy:\n        version: 2026.001.0001\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	if err := appendCandyPackages("foo", "rpm", []string{"ripgrep", "ripgrep"}); err != nil {
		t.Fatalf("appendCandyPackages: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(candyDir, kit.UnifiedFileName))
	var root map[string]any
	if err := yaml.Unmarshal(data, &root); err != nil {
		t.Fatalf("re-parse: %v\n%s", err, data)
	}
	if _, stray := root["rpm"]; stray {
		t.Fatalf("stray top-level rpm: introduced\n%s", data)
	}
	if _, stray := root["distro"]; stray {
		t.Fatalf("stray top-level distro: introduced (must be under the candy body)\n%s", data)
	}
	candy := root["foo"].(map[string]any)["candy"].(map[string]any)
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

// TestCandySet_DescendsIntoCandyBody guards that `candy set version X` writes the entity's
// candy.version, never a stray top-level version:.
func TestCandySet_DescendsIntoCandyBody(t *testing.T) {
	dir := t.TempDir()
	candyDir := filepath.Join(dir, kit.DefaultCandyDir, "bar")
	if err := os.MkdirAll(candyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(candyDir, kit.UnifiedFileName),
		[]byte("bar:\n    candy:\n        version: 2026.001.0001\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	if err := candySet("bar", "version", "2026.186.0000"); err != nil {
		t.Fatalf("candySet: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(candyDir, kit.UnifiedFileName))
	var root map[string]any
	if err := yaml.Unmarshal(data, &root); err != nil {
		t.Fatalf("re-parse: %v\n%s", err, data)
	}
	if _, stray := root["version"]; stray {
		t.Fatalf("stray top-level version: introduced (must be under candy:)\n%s", data)
	}
	if got := root["bar"].(map[string]any)["candy"].(map[string]any)["version"]; got != "2026.186.0000" {
		t.Fatalf("bar.candy.version not set, got %v\n%s", got, data)
	}
}
