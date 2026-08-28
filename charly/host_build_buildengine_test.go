package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencharly/spec/spec"
)

// host_build_buildengine_test.go — the B12 regression gate for the #326 fix: a plugin
// candy that fails to COMPILE must be a FATAL error from hostBuildConnectPlugins, not a
// warning the build continues past. Before the fix the connect leg printed
// "warning: build-time plugin load: ..." and returned an empty map, so the build died
// LATER on a 'no provider registered' error naming the wrong cause. This test asserts
// the error is returned immediately with the plugin's name in it — it FAILS against the
// pre-fix behavior (which returned nil).
func TestHostBuildConnectPlugins_BrokenPluginIsFatal(t *testing.T) {
	dir := t.TempDir()
	must := func(p, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Project root: the candy/ dir is discovered (the plugin candy lives there).
	must(filepath.Join(dir, "charly.yml"), `version: "`+latestSchemaVersion.String()+`"
defaults:
  registry: ghcr.io/example
discover:
  - path: candy
    recursive: true
`)
	// The out-of-tree plugin candy whose go module FAILS to compile.
	must(filepath.Join(dir, "candy", "broken-plugin", "charly.yml"), `broken-plugin:
  candy:
    version: "2026.150.0000"
    description: a plugin candy whose go module fails to compile
    plugin:
      source: github.com/opencharly/broken-plugin
      providers:
        - verb:testverb
    plan:
      - check: the broken plugin is discovered
        mkdir: /tmp/ok
`)
	// A go module that cannot build: an unterminated function body is a hard
	// compile error (deterministic, no network).
	must(filepath.Join(dir, "candy", "broken-plugin", "go.mod"), "module github.com/opencharly/broken-plugin\n\ngo 1.26.4\n")
	must(filepath.Join(dir, "candy", "broken-plugin", "main.go"), "package main\n\nfunc main() {\n")

	// The work references the plugin's verb, so loadProjectPlugins MUST try to
	// build+connect it and fail.
	_, err := hostBuildConnectPlugins(context.Background(), spec.ResolvedProjectRequest{
		Dir:            dir,
		ExtraCandyRefs: []string{"testverb"},
	}, buildEngineContext{})
	if err == nil {
		t.Fatal("hostBuildConnectPlugins must return a FATAL error for a plugin candy that fails to compile (it must NOT continue to a later 'no provider registered')")
	}
	// The actionable error names the plugin and the compile failure.
	if !strings.Contains(err.Error(), "broken-plugin") {
		t.Fatalf("error must name the failing plugin, got: %v", err)
	}
	if !strings.Contains(err.Error(), "go build") {
		t.Fatalf("error must surface the go build failure, got: %v", err)
	}
}
