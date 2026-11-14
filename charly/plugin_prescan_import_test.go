package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
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

// A LOCAL-PATH `import:` entry may name a whole PROJECT DIRECTORY — the umbrella's
// `- strata-cli: ./plugin-strata`, and every `- <ns>: ./<repo>` entry beside it. Such an
// imported project's plugin words do NOT live in its root charly.yml: a plugin repo carries
// `discover: [{path: candy, recursive: true}]` at its root and declares its providers in
// `candy/<plugin>/charly.yml`, so the ONLY way to reach them is to walk that project's own
// `discover:` tree — exactly what the root file's own discover walk does.
//
// The import leg treated EVERY non-@ ref as a bare sibling FILE (`- vm.yml`), so for a
// directory entry `os.ReadFile(<dir>)` failed and the tree was never walked. The plugin's
// candy entities still loaded (the project walk reaches them), but its COMMAND word never
// reached registerDeclaredExternalCommand before kong.Parse, so from the umbrella
// `charly strata --help` exited 80 with `unexpected argument strata` — while the same word
// resolved from the plugin's own repo root (where its discover: IS the root file's walk),
// and from a project composing the same candy by @github ref (the remote leg). Both halves
// of the asymmetry are asserted here: the command word AND the kind word.
func TestPrescan_LocalPathImportWalksItsDiscoverTree(t *testing.T) {
	const (
		cmdWord  = "plugword"
		kindWord = "plugkind"
	)
	dir := t.TempDir()
	rootDir := filepath.Join(dir, "root")
	pluginRepo := filepath.Join(dir, "plugin-fixture")

	// The fixture plugin repo: a root charly.yml that only DECLARES where its manifests
	// live, and a candy carrying the plugin: block — the real plugin-repo shape.
	if err := os.MkdirAll(filepath.Join(pluginRepo, "candy", "plug"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginRepo, "charly.yml"), []byte(
		"repo: github.com/opencharly/plug\ndiscover:\n    - path: candy\n      recursive: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pluginManifest := `plug:
    candy:
        description: a fixture plugin candy declaring a command and a kind word.
        plugin:
            source: github.com/opencharly/plug/candy/plug
            providers:
                - command:` + cmdWord + `
                - kind:` + kindWord + `
        plan:
            - check: command=true
              id: plug-check
              context:
                  - build
              command: "true"
`
	if err := os.WriteFile(filepath.Join(pluginRepo, "candy", "plug", "charly.yml"), []byte(pluginManifest), 0o644); err != nil {
		t.Fatal(err)
	}

	// The composing project imports that repo by LOCAL PATH (the umbrella's shape).
	if err := os.MkdirAll(rootDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rootYAML := "import:\n    - plug: " + pluginRepo + "\n"
	rootPath := filepath.Join(rootDir, "charly.yml")
	if err := os.WriteFile(rootPath, []byte(rootYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	root, err := os.ReadFile(rootPath)
	if err != nil {
		t.Fatal(err)
	}

	clearPrescanFixtureWords(cmdWord, kindWord)
	t.Cleanup(func() { clearPrescanFixtureWords(cmdWord, kindWord) })

	prescanDeclaredPluginWords(root, rootDir)

	if _, ok := declaredExternalCommandIdentities()[providerKey(ClassCommand, cmdWord, "")]; !ok {
		t.Fatal("the local-path imported project's discover: tree was never walked — its candy's " +
			"command word never reached the pre-parse grammar (the `unexpected argument " + cmdWord + "` defect)")
	}
	if !isDeclaredExternalKind(kindWord) {
		t.Fatal("the local-path imported project's discover: tree was never walked — its candy's kind word was not registered")
	}
}

// An import GRAPH may revisit a directory: a repeated entry (two aliases for one project) and a
// cycle (`alpha` imports `beta`, `beta` imports `alpha` back — the real main↔cachyos shape).
// Registration is idempotent, but the WALK must visit each project once, or a self-import never
// terminates. The goroutine guard turns a non-terminating walk into a fast, named failure
// instead of a package-timeout hang — it is a hang detector, not synchronization.
func TestPrescan_RepeatedAndCyclicImportsTerminate(t *testing.T) {
	const (
		alphaWord = "cyclealphaword"
		betaWord  = "cyclebetaword"
	)
	dir := t.TempDir()
	rootDir := filepath.Join(dir, "root")
	alphaDir := filepath.Join(dir, "cycle-alpha")
	betaDir := filepath.Join(dir, "cycle-beta")

	writePrescanProject := func(projectDir, manifest string) {
		if err := os.MkdirAll(projectDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(projectDir, "charly.yml"), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writePrescanCandy := func(projectDir, candyName, providerWord string) {
		candyDir := filepath.Join(projectDir, "candy", candyName)
		if err := os.MkdirAll(candyDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(candyDir, "charly.yml"), []byte(
			"candy:\n    description: a cycle fixture candy.\n    plugin:\n        providers:\n            - kind:"+providerWord+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// alpha imports beta; beta imports alpha back, so each project is reachable from the other.
	writePrescanProject(alphaDir, "repo: github.com/opencharly/cycle-alpha\ndiscover:\n    - path: candy\n      recursive: true\nimport:\n    - beta: ../cycle-beta\n")
	writePrescanProject(betaDir, "repo: github.com/opencharly/cycle-beta\ndiscover:\n    - path: candy\n      recursive: true\nimport:\n    - alpha: ../cycle-alpha\n")
	writePrescanCandy(alphaDir, "alphaprobe", alphaWord)
	writePrescanCandy(betaDir, "betaprobe", betaWord)
	if err := os.MkdirAll(rootDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// The SAME project imported twice under two aliases — a repeated (diamond) entry.
	rootYAML := "import:\n    - alpha: " + alphaDir + "\n    - alpha-again: " + alphaDir + "\n"
	if err := os.WriteFile(filepath.Join(rootDir, "charly.yml"), []byte(rootYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	root, err := os.ReadFile(filepath.Join(rootDir, "charly.yml"))
	if err != nil {
		t.Fatal(err)
	}

	clearPrescanFixtureWords(alphaWord, betaWord)
	t.Cleanup(func() { clearPrescanFixtureWords(alphaWord, betaWord) })

	done := make(chan struct{})
	go func() {
		defer close(done)
		prescanDeclaredPluginWords(root, rootDir)
	}()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("the import walk did not terminate: a repeated import plus a cycle (alpha→beta→alpha) must terminate")
	}

	for _, w := range []string{alphaWord, betaWord} {
		if !isDeclaredExternalKind(w) {
			t.Fatalf("cyclic import graph: %q was never registered", w)
		}
	}
}

// clearPrescanFixtureWords drops the fixture words a prescan test registered, so the
// process-wide additive prescan maps do not leak into another test (the same
// delete-in-cleanup isolation the kind-prescan tests use).
func clearPrescanFixtureWords(kindsOrCommands ...string) {
	declaredDeployMu.Lock()
	defer declaredDeployMu.Unlock()
	for _, w := range kindsOrCommands {
		delete(declaredKind, w)
		delete(declaredExternalCommand, providerKey(ClassCommand, w, ""))
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
