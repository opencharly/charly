package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestPrescanRemoteLegRunsWithoutDiscoverBlock is the regression test for the remote-ref
// leg being gated on a LOCAL discover: block.
//
// The two are unrelated: the remote leg reads @github plugin refs, discover: walks
// in-repo directories. While the leg sat behind `len(doc.Discover) == 0`, a project that
// pins a plugin remotely and discovers nothing locally never prescanned that plugin, so
// its `plugin: primary:` never reached the parse and the scalar shorthand was rejected —
// for a candy that declares exactly that field, and fatally for the whole document.
//
// The repo resolution is stubbed so the test asserts the ROUTING (does the leg run) with
// no network: that is precisely what regressed.
func TestPrescanRemoteLegRunsWithoutDiscoverBlock(t *testing.T) {
	const word = "prescanremoteverb"
	if _, ok := pluginPrimaries[word]; ok {
		t.Fatalf("precondition: %q must not already have a registered primary", word)
	}

	// A fetched plugin repo, as the resolver would hand back: <root>/candy/plugin-<name>/charly.yml.
	repo := t.TempDir()
	candyDir := filepath.Join(repo, "candy", "plugin-prescanremote")
	if err := os.MkdirAll(candyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `plugin-prescanremote:
    candy:
        version: 2026.242.0001
        description: a prescan test plugin candy declaring a scalar-sugar primary.
        plugin:
            source: github.com/opencharly/plugin-prescanremote/candy/plugin-prescanremote
            providers:
                - verb:` + word + `
            primary:
                ` + word + `: method
        plan:
            - check: command=true
              id: prescanremote-check
              context:
                  - build
              command: "true"
`
	if err := os.WriteFile(filepath.Join(candyDir, "charly.yml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	orig := resolveRemotePluginRepo
	t.Cleanup(func() { resolveRemotePluginRepo = orig })
	resolved := false
	resolveRemotePluginRepo = func(repoPath, _ string) (string, error) {
		resolved = true
		return repo, nil
	}

	// A root manifest that pins the plugin remotely and carries NO discover: block —
	// the exact shape that used to skip the remote leg entirely.
	root := []byte(`version: 2026.248.1030
probe-img:
    candy:
        version: 2026.242.0001
        description: pins the plugin by @github ref, discovers nothing locally.
        base: docker.io/library/alpine:3.20
        candy:
            - '@github.com/opencharly/plugin-prescanremote/candy/plugin-prescanremote:v2026.242.1'
`)
	prescanDeclaredPluginWords(root, t.TempDir())

	if !resolved {
		t.Fatal("the remote leg never ran: a root manifest with no discover: block skipped it")
	}
	if got := pluginPrimaries[word]; got != "method" {
		t.Fatalf("primary for %q = %q, want %q — the remote candy's plugin.primary: never reached the parse", word, got, "method")
	}
}
