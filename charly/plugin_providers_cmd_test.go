package main

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPluginProvidersCmd extracts a candy's plugin.providers from its charly.yml, single-source
// for the host `.providers` manifest the PKGBUILD bakes. The fixture's DESCRIPTION prose also
// mentions `verb:credential` / `command:secrets` inline, proving the structured (collectPluginProviders)
// extraction does NOT over-match prose the way a naive grep would.
func TestPluginProvidersCmd(t *testing.T) {
	dir := t.TempDir()
	manifest := `plugin-secrets:
    candy:
        version: 2026.178.2100
        description: |-
            Serves verb:credential and command:secrets — go-keyring lives here.
    plugin-secrets-decl:
        plugin:
            source: github.com/opencharly/charly/candy/plugin-secrets
            providers:
                - verb:credential
                - command:secrets
    secrets-plugin-builds:
        check: the plugin builds
        context:
            - build
        plugin: command
        plugin_input:
            command: "true"
`
	if err := os.WriteFile(filepath.Join(dir, UnifiedFileName), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	got := captureStdout(t, func() {
		if err := (&PluginProvidersCmd{Dir: dir}).Run(); err != nil {
			t.Fatalf("Run: %v", err)
		}
	})

	want := "verb:credential\ncommand:secrets\n"
	if got != want {
		t.Errorf("providers manifest = %q, want %q", got, want)
	}
}

// captureStdout runs f with os.Stdout redirected to a pipe and returns what it wrote.
func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		var b strings.Builder
		sc := bufio.NewScanner(r)
		for sc.Scan() {
			b.WriteString(sc.Text())
			b.WriteByte('\n')
		}
		done <- b.String()
	}()
	f()
	_ = w.Close()
	os.Stdout = orig
	return <-done
}
