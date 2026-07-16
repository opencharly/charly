package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestPluginsGenReproducible is the drift gate for the committed compiled-in plugin
// wiring: it regenerates plugins_generated.go + go.work from charly.yml's
// `compiled_plugins:` and asserts the committed files match byte-for-byte. It fails if
// someone hand-edits a generated file, or changes compiled_plugins without re-running
// `task build:binary` (which runs pluginsgen). Mirrors spec.TestGenReproducible for
// the CUE-gen path.
func TestPluginsGenReproducible(t *testing.T) {
	root := filepath.Join("..", "..", "..") // charly/internal/pluginsgen -> repo root
	genGo, genWork, err := generate(root, filepath.Join("charly", "charly.yml"))
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	for _, tc := range []struct {
		rel string
		got []byte
	}{
		{filepath.Join("charly", "plugins_generated.go"), genGo},
		{"go.work", genWork},
	} {
		committed, err := os.ReadFile(filepath.Join(root, tc.rel))
		if err != nil {
			t.Fatalf("read committed %s: %v", tc.rel, err)
		}
		if string(committed) != string(tc.got) {
			t.Errorf("%s is stale — re-run `task build:binary` (pluginsgen) and commit it.\n--- committed ---\n%s\n--- regenerated ---\n%s",
				tc.rel, committed, tc.got)
		}
	}
}
