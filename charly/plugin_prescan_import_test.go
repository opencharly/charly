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

// A FLAT import entry (`- vm.yml`) is a bare-string ref, not the `alias: ref` map shape.
// Decoding `import:` as []map[string]string made the WHOLE struct unmarshal fail on the
// first bare string, so the early return skipped the discover walk AND the import leg: a
// project with a flat import plus any plugin-provided kind failed to parse EVERY document
// with "no kind discriminator" (the kind word was never prescanned).
//
// This test proves BOTH halves of the import leg:
//   - the DECODE fix (spec.ImportList) lets the walk run at all — asserted via the
//     discovered manifest's declared word; and
//   - the FLAT-file read registers the sibling's OWN declared word — `flatprobe` lives
//     ONLY inside the flat-imported file, so deleting the `!strings.HasPrefix(ref, "@")`
//     branch (which the decode-only fix would survive) fails this assertion.
func TestPrescan_FlatImportDoesNotAbortTheWalk(t *testing.T) {
	dir := t.TempDir()
	// A flat sibling file imported by bare name (the per-kind split shape). It carries its
	// OWN plugin declaration — the word `flatprobe` is reachable ONLY by reading this file.
	if err := os.WriteFile(filepath.Join(dir, "vm.yml"), []byte(
		"flat-vm:\n    candy:\n        plugin:\n            providers:\n                - kind:flatprobe\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A discovered candy manifest that declares a DIFFERENT external kind word.
	candy := filepath.Join(dir, "candy", "decl")
	if err := os.MkdirAll(candy, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(candy, "charly.yml"), []byte(
		"decl:\n    candy:\n        plugin:\n            providers:\n                - kind:prescanprobe\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	root := []byte("import:\n    - vm.yml\ndiscover:\n    - path: candy\n      recursive: true\n")

	for _, w := range []string{"prescanprobe", "flatprobe"} {
		declaredDeployMu.Lock()
		delete(declaredKind, w)
		declaredDeployMu.Unlock()
	}
	t.Cleanup(func() {
		for _, w := range []string{"prescanprobe", "flatprobe"} {
			declaredDeployMu.Lock()
			delete(declaredKind, w)
			declaredDeployMu.Unlock()
		}
	})

	prescanDeclaredPluginWords(root, dir)

	if !isDeclaredExternalKind("prescanprobe") {
		t.Fatal("discover walk was skipped on a flat-import project — the declared kind word was never registered")
	}
	// The FLAT branch's own coverage: without it this word never registers.
	if !isDeclaredExternalKind("flatprobe") {
		t.Fatal("the flat-imported sibling file's own plugin declaration was never prescanned — the flat branch is missing")
	}
}
