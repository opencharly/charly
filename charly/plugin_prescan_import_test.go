package main

import (
	"os"
	"path/filepath"
	"testing"
)

// A project's `import:` namespaces resolve to WHOLE PROJECTS — a distro repo, or the local
// box/<name> submodule the ref maps to — and an imported box's beds author plugin-verb steps
// exactly like the root file's do. The scalar shorthand (`cstream: status`) desugars at PARSE
// time, before any provider can connect, from the primary the prescan registers.
//
// The prescan walked the root file and its `discover:` paths and NOT the imports, so a verb
// used only by an imported box never had its primary registered. The result was an asymmetry
// that reads as a broken bed when the bed is fine: the SAME manifest validated at exit 0 from
// its own repo root (where it IS the root file) and failed composed into charly with
//
//	parse: node "check-cstream-pod": plan[29] plugin verb "cstream" takes a MAP input
//	(it declares no primary field for the scalar shorthand)
//
// It took `charly box validate` on charly's own main to exit 1.
func TestPrescan_ImportedProjectManifestIsPrescanned(t *testing.T) {
	dir := t.TempDir()
	imported := filepath.Join(dir, "box", "somedistro")
	if err := os.MkdirAll(imported, 0o755); err != nil {
		t.Fatal(err)
	}
	// The imported project pins a plugin candy. Only a prescan that READS this file can find
	// the ref at all — it appears nowhere in the root manifest.
	if err := os.WriteFile(filepath.Join(imported, "charly.yml"), []byte(
		"somebox:\n    pod:\n        candy:\n            - '@github.com/opencharly/plugin-imported/candy/plugin-imported:v2026.100.1200'\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	root := []byte("import:\n    - somedistro: '@github.com/opencharly/distro-somedistro:v2026.100.1200'\n")

	var seen []string
	origResolve := resolveImportedProject
	origRemote := resolveRemotePluginRepo
	t.Cleanup(func() { resolveImportedProject = origResolve; resolveRemotePluginRepo = origRemote })

	resolveImportedProject = func(name, ref, baseDir string) (string, error) {
		return filepath.Join(baseDir, "box", name), nil
	}
	resolveRemotePluginRepo = func(repoPath, baseDir string) (string, error) {
		seen = append(seen, repoPath)
		return "", os.ErrNotExist // the fetch itself is not under test
	}

	prescanDeclaredPluginWords(root, dir)

	// The imported manifest's plugin ref must reach the remote leg. Without the import leg it
	// never does: the root file names no plugin at all.
	for _, got := range seen {
		if got == "github.com/opencharly/plugin-imported" {
			return
		}
	}
	t.Errorf("imported project's plugin ref was never prescanned; remote leg saw %v", seen)
}

// The parse reads an imported box from its LOCAL submodule checkout when one exists (a failing
// box names box/<name>/charly.yml, not a cache dir). The prescan must read the SAME tree: the
// gitlink and the import pin drift routinely, and prescanning the pinned cache would register
// primaries from a different manifest than the one whose steps are about to be desugared.
func TestResolveImportedProject_PrefersLocalCheckout(t *testing.T) {
	dir := t.TempDir()
	local := filepath.Join(dir, "box", "mydistro")
	if err := os.MkdirAll(local, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(local, "charly.yml"), []byte("x: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := resolveImportedProject("mydistro", "github.com/opencharly/distro-mydistro:v2026.100.1200", dir)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != local {
		t.Errorf("resolved %q, want the local checkout %q", got, local)
	}
}
