package main

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestCandySet_DescendsIntoCandyWrapper guards that `charly candy set` edits
// candy.<dotpath> INSIDE the kind-keyed `candy:` wrapper, never a stray
// top-level key or a phantom `layer:` map. The box/candy rename moved the
// layer kind key from `layer:` to `candy:`; before this fix the command still
// prepended `layer.`, so setting `version` left the real candy.version stale
// and appended a `layer:` map the loader ignores.
func TestCandySet_DescendsIntoCandyWrapper(t *testing.T) {
	dir := t.TempDir()
	candyDir := filepath.Join(dir, "candy", "foo")
	if err := os.MkdirAll(candyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	const start = "candy:\n  name: foo\n  version: 2026.001.0001\n"
	if err := os.WriteFile(filepath.Join(candyDir, UnifiedFileName), []byte(start), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	if err := (&CandySetCmd{Name: "foo", Path: "version", Value: "2026.002.0002"}).Run(); err != nil {
		t.Fatalf("CandySetCmd.Run: %v", err)
	}

	out, err := os.ReadFile(filepath.Join(candyDir, UnifiedFileName))
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := yaml.Unmarshal(out, &root); err != nil {
		t.Fatalf("re-parsing result: %v\n%s", err, out)
	}
	if _, hasTop := root["version"]; hasTop {
		t.Fatalf("stray top-level version: introduced (the bug):\n%s", out)
	}
	if _, hasCandy := root["layer"]; hasCandy {
		t.Fatalf("stray `layer:` wrapper introduced (pre-rename bug):\n%s", out)
	}
	candy, ok := root["candy"].(map[string]any)
	if !ok {
		t.Fatalf("candy: wrapper missing or wrong type:\n%s", out)
	}
	if got := candy["version"]; got != "2026.002.0002" {
		t.Fatalf("candy.version = %v, want 2026.002.0002:\n%s", got, out)
	}

	// An already-qualified path must not be double-prefixed (candy.candy.name).
	if err := (&CandySetCmd{Name: "foo", Path: "candy.name", Value: "bar"}).Run(); err != nil {
		t.Fatalf("CandySetCmd.Run (candy.-prefixed): %v", err)
	}
	out2, err := os.ReadFile(filepath.Join(candyDir, UnifiedFileName))
	if err != nil {
		t.Fatal(err)
	}
	root = nil
	if err := yaml.Unmarshal(out2, &root); err != nil {
		t.Fatalf("re-parsing result 2: %v\n%s", err, out2)
	}
	candy, _ = root["candy"].(map[string]any)
	if got := candy["name"]; got != "bar" {
		t.Fatalf("candy.name = %v, want bar (candy.-prefixed path mishandled?):\n%s", got, out2)
	}
}
