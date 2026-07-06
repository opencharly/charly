package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestPrescanExternalDeploySubstrate proves the loader pre-load (plugin_prescan.go):
// a project whose candy declares `plugin: providers: [deploy:<word>]` parses a
// `<bed>: { <word>: {…} }` node into BundleNode{Target: <word>} at LOAD time —
// WITHOUT the out-of-process provider being built/connected. The word is recognized
// purely from the declaration pre-scan; the real provider would connect later at
// loadProjectPlugins. Uses a UNIQUE word so it can never collide with another test's
// real provider registration (e.g. the e2e's "exampledeploy").
func TestPrescanExternalDeploySubstrate(t *testing.T) {
	const word = "prescantestdeploy"
	if _, ok := providerRegistry.ResolveDeploy(word); ok {
		t.Fatalf("precondition: %q must NOT be a connected provider", word)
	}

	dir := t.TempDir()
	candyDir := filepath.Join(dir, "candy", "prescan-plugin")
	if err := os.MkdirAll(candyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	candyYAML := `prescan-plugin:
    candy:
        version: 2026.175.0001
        description: a pre-scan test plugin candy declaring an external deploy word.
        plugin:
            providers:
                - deploy:` + word + `
            source: github.com/example/repo/candy/prescan-plugin
        plan:
            - check: command=true
              id: prescan-check
              context:
                  - build
              command: "true"
`
	if err := os.WriteFile(filepath.Join(candyDir, "charly.yml"), []byte(candyYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	rootYAML := `version: ` + LatestSchemaVersion().String() + `
discover:
    - path: candy
      recursive: true
check-prescan:
    ` + word + `:
        disposable: true
        description: external deploy substrate recognized via the pre-scan only.
        add_candy:
            - candy/prescan-plugin
        plan:
            - check: command=true
              id: prescan-bed-check
              context:
                  - runtime
              command: "true"
`
	if err := os.WriteFile(filepath.Join(dir, "charly.yml"), []byte(rootYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	uf, _, err := LoadUnified(dir)
	if err != nil {
		t.Fatalf("LoadUnified must parse the external-deploy-substrate bed via the pre-scan: %v", err)
	}
	node, ok := uf.Bundle["check-prescan"]
	if !ok {
		t.Fatalf("bed check-prescan not parsed into uf.Bundle (have %d entries)", len(uf.Bundle))
	}
	if node.Target != word {
		t.Fatalf("bed Target = %q, want %q (external substrate routed to the bundle builder)", node.Target, word)
	}
	// The parse succeeded purely from the declaration pre-scan — the word is still
	// NOT a connected provider.
	if _, ok := providerRegistry.ResolveDeploy(word); ok {
		t.Fatalf("%q became a connected provider — the test must prove parse WITHOUT connection", word)
	}
	if !recognizedDeploySubstrate(word) {
		t.Fatalf("pre-scan should have registered %q as a recognized substrate", word)
	}
}
