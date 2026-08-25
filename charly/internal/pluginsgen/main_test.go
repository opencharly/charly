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

// TestPluginModulePath_SourceDriven verifies the module path of record is the
// candy's `plugin.source:` field when it names a module path (the cutover's
// standalone shape), not the candy's go.mod.
func TestPluginModulePath_SourceDriven(t *testing.T) {
	dir := t.TempDir()
	writeCandy(t, dir, map[string]string{
		"charly.yml": "my-plugin:\n    candy:\n        plugin:\n            source: github.com/opencharly/plugin-port/candy/plugin-port\n",
		"go.mod":     "module github.com/opencharly/charly/candy/plugin-port\n\ngo 1.26.4\n",
	})
	mod, err := pluginModulePath(dir, "my-plugin")
	if err != nil {
		t.Fatalf("pluginModulePath: %v", err)
	}
	if want := "github.com/opencharly/plugin-port/candy/plugin-port"; mod != want {
		t.Fatalf("source-driven module path: got %q want %q", mod, want)
	}
}

// TestPluginModulePath_BuiltinFallback pins the `source: builtin` fallback: the
// module path comes from candy/<name>/go.mod when source is builtin (the in-repo
// state for plugin-agentteams / plugin-dsh).
func TestPluginModulePath_BuiltinFallback(t *testing.T) {
	dir := t.TempDir()
	writeCandy(t, dir, map[string]string{
		"charly.yml": "my-plugin:\n\n    candy:\n        plugin:\n            source: builtin\n",
		"go.mod":     "module github.com/opencharly/charly/candy/my-plugin\n\ngo 1.26.4\n",
	})
	mod, err := pluginModulePath(dir, "my-plugin")
	if err != nil {
		t.Fatalf("pluginModulePath: %v", err)
	}
	if want := "github.com/opencharly/charly/candy/my-plugin"; mod != want {
		t.Fatalf("builtin fallback: got %q want %q", mod, want)
	}
}

// TestPluginModulePath_SiblingSkillEntityNoSource pins that a candy file with a
// sibling `<name>-cli-skill` entity (no plugin.source on the candy itself) still
// resolves the module from go.mod — the multi-top-key shape plugin-dsh,
// plugin-agentteams and plugin-ollama carry.
func TestPluginModulePath_SiblingSkillEntityNoSource(t *testing.T) {
	dir := t.TempDir()
	writeCandy(t, dir, map[string]string{
		"charly.yml": "my-plugin:\n\n    candy:\n        version: 2026.193.0900\n        plugin:\n            providers:\n                - command:dsh\nmy-plugin-cli-skill:\n    skill:\n        name: my-plugin-cli-skill\n",
		"go.mod":     "module github.com/opencharly/charly/candy/my-plugin\n\ngo 1.26.4\n",
	})
	mod, err := pluginModulePath(dir, "my-plugin")
	if err != nil {
		t.Fatalf("pluginModulePath: %v", err)
	}
	if want := "github.com/opencharly/charly/candy/my-plugin"; mod != want {
		t.Fatalf("no-source fallback: got %q want %q", mod, want)
	}
}

func writeCandy(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
