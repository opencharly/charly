package candy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/spec"
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
	if err := os.WriteFile(filepath.Join(candyDir, spec.UnifiedFileName),
		[]byte("foo:\n    candy:\n        version: 2026.001.0001\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	if err := appendCandyPackages("foo", "rpm", []string{"ripgrep", "ripgrep"}); err != nil {
		t.Fatalf("appendCandyPackages: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(candyDir, spec.UnifiedFileName))
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
	if err := os.WriteFile(filepath.Join(candyDir, spec.UnifiedFileName),
		[]byte("bar:\n    candy:\n        version: 2026.001.0001\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	if err := candySet("bar", "version", "2026.186.0000"); err != nil {
		t.Fatalf("candySet: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(candyDir, spec.UnifiedFileName))
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

// TestAddFormatVerbs_DerivedFromOneSource guards the property that made adding a
// package format error-prone: the set of `add-<fmt>` verbs, the dispatch, and the
// usage string used to be three separate hand-maintained lists. They now all derive
// from sectionDistroPath, so this test fails if a row is added without the verb
// becoming dispatchable and appearing in the usage line.
func TestAddFormatVerbs_DerivedFromOneSource(t *testing.T) {
	for section := range sectionDistroPath {
		verb := "add-" + section
		if !strings.Contains(candyUsage, verb) {
			t.Errorf("usage string omits %q (it must derive from sectionDistroPath): %s", verb, candyUsage)
		}
		// A dispatchable verb reports the per-verb arity error, never "unknown
		// candy subcommand" — that distinguishes "routed" from "fell through".
		err := runCandyCLI([]string{verb})
		if err == nil || strings.Contains(err.Error(), "unknown candy subcommand") {
			t.Errorf("%s is not dispatched (err=%v)", verb, err)
		}
	}
	// Presence control: a format with no row must still be rejected, so the
	// check above cannot be satisfied by accepting everything.
	if err := runCandyCLI([]string{"add-nope", "foo", "pkg"}); err == nil ||
		!strings.Contains(err.Error(), "unknown candy subcommand") {
		t.Errorf("an unmapped add-<fmt> must be rejected, got %v", err)
	}
}

// TestAppendCandyPackages_Apk pins the alpine target: add-apk writes to
// distro.alpine.package, the section the embedded alpine distro vocabulary reads.
func TestAppendCandyPackages_Apk(t *testing.T) {
	dir := t.TempDir()
	candyDir := filepath.Join(dir, kit.DefaultCandyDir, "foo")
	if err := os.MkdirAll(candyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(candyDir, spec.UnifiedFileName),
		[]byte("foo:\n    candy:\n        version: 2026.001.0001\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	if err := appendCandyPackages("foo", "apk", []string{"sudo"}); err != nil {
		t.Fatalf("appendCandyPackages(apk): %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(candyDir, spec.UnifiedFileName))
	var root map[string]any
	if err := yaml.Unmarshal(data, &root); err != nil {
		t.Fatalf("re-parse: %v\n%s", err, data)
	}
	candy := root["foo"].(map[string]any)["candy"].(map[string]any)
	distro, ok := candy["distro"].(map[string]any)
	if !ok {
		t.Fatalf("candy.distro missing\n%s", data)
	}
	alpine, ok := distro["alpine"].(map[string]any)
	if !ok {
		t.Fatalf("candy.distro.alpine missing (add-apk → distro.alpine)\n%s", data)
	}
	if pkgs := alpine["package"].([]any); len(pkgs) != 1 || pkgs[0] != "sudo" {
		t.Fatalf("want distro.alpine.package=[sudo], got %v", pkgs)
	}
}
