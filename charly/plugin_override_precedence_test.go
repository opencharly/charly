package main

// plugin_override_precedence_test.go — the OVERRIDE-PRECEDENCE contract of the plugin-binary
// resolver (charly #587's named follow-up wave, step 3/3): a local CHARLY_REPO_OVERRIDE tree for
// a plugin's REPO outranks a baked provider binary, and core asks that question ONLY through the
// spec.ProjectLoader seam — never by re-parsing the env (import_purity_test.go + R3).
//
// #587's first arm answered the same question by re-parsing CHARLY_REPO_OVERRIDE in core (a
// directory-prefix match) and was withdrawn for exactly that: loaderkit.RepoOverrideDir owns the
// ONE parse. The cases below pin the seam form — the root arrives from
// spec.ProjectLoader.RepoOverrideDir, is keyed by the plugin's REPO (pluginRepoPath, parsed from
// the candy's source ref), outranks the baked search, and a seam ERROR (a malformed or missing
// override target) fails the plugin load loud instead of silently serving other bytes.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencharly/spec/proc"
	"github.com/opencharly/spec/spec"
)

// fakeOverrideLoader is a spec.ProjectLoader whose ONLY implemented method is the override seam.
// The embedded interface is nil, so any OTHER seam method called on it panics — a test that
// accidentally routes different work here fails loudly rather than silently passing.
type fakeOverrideLoader struct {
	spec.ProjectLoader
	root  string
	found bool
	err   error
	asked []string
}

func (f *fakeOverrideLoader) RepoOverrideDir(repoPath string) (string, bool, error) {
	f.asked = append(f.asked, repoPath)
	return f.root, f.found, f.err
}

// swapProjectLoader installs l as the registered loader for the duration of one test.
func swapProjectLoader(t *testing.T, l spec.ProjectLoader) {
	t.Helper()
	saved := activeProjectLoader
	activeProjectLoader = l
	t.Cleanup(func() { activeProjectLoader = saved })
}

// TestPluginRepoPath_NamesTheRepoOfAPluginSource: the seam is asked about the plugin's REPO, so
// the derivation from the candy's source: declaration must yield exactly the key an override
// entry matches (spec.ParseRemoteRef — the same parse the remote-image resolve path uses). A
// source that names no remote repo (a ./-relative or absolute path, a bare single-segment name)
// yields "": nothing for an override to match, and the seam is never asked.
//
// The SHORT form (`owner/repo`, with or without a candy sub-path) is included deliberately: a
// bare owner/repo LHS is what the seam's OWN normalization prefixes github.com to, so a candy
// source written that way must name github.com/owner/repo — deriving "" there left the override
// silently unasked and unapplied (the B14(c) divergence caught on #592). `candy/plugin-local`
// (a two-segment RELATIVE dir) normalizes the same way, which is consistent: an operator writing
// that exact LHS into CHARLY_REPO_OVERRIDE gets the same `github.com/candy/plugin-local` key, so
// the two sides agree instead of diverging.
func TestPluginRepoPath_NamesTheRepoOfAPluginSource(t *testing.T) {
	for _, tc := range []struct{ source, want string }{
		{"github.com/opencharly/plugin-h4/candy/plugin-h4", "github.com/opencharly/plugin-h4"},
		{"@github.com/opencharly/plugin-h4/candy/plugin-h4:v2026.237.1417", "github.com/opencharly/plugin-h4"},
		{"opencharly/plugin-h4", "github.com/opencharly/plugin-h4"},
		{"@opencharly/plugin-h4", "github.com/opencharly/plugin-h4"},
		{"opencharly/plugin-h4/candy/plugin-h4", "github.com/opencharly/plugin-h4"},
		{"opencharly/plugin-h4/candy/plugin-h4:v2026.237.1417", "github.com/opencharly/plugin-h4"},
		{"candy/plugin-local", "github.com/candy/plugin-local"},
		{"./candy/plugin-local", ""},
		{"../candy/plugin-local", ""},
		{"/abs/candy/plugin-local", ""},
		{"plugin-local", ""},
		{"", ""},
	} {
		if got := pluginRepoPath(tc.source); got != tc.want {
			t.Errorf("pluginRepoPath(%q) = %q, want %q", tc.source, got, tc.want)
		}
	}
}

// TestResolvePluginBinary_OverrideOutranksBakedBinary is the wave's core claim: when the seam
// reports the plugin's repo as overridden, the BAKED binary MUST NOT serve — the bytes come from
// the override tree — and both the bypass and the served tree are named on stderr.
func TestResolvePluginBinary_OverrideOutranksBakedBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("the override arm host-builds a real module")
	}
	bakedDir := t.TempDir()
	bakedBin := filepath.Join(bakedDir, "plugin-h4")
	if err := os.WriteFile(bakedBin, []byte("prebuilt"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeProviders(t, bakedDir, "plugin-h4", "verb:h4")
	cache := t.TempDir()
	isolatePluginSearch(t, bakedDir)
	t.Setenv("XDG_CACHE_HOME", cache)

	root := t.TempDir()
	srcDir := writeMinimalPluginModule(t, root, "plugin-h4")
	const repo = "github.com/opencharly/plugin-h4"
	loader := &fakeOverrideLoader{root: root, found: true}
	swapProjectLoader(t, loader)

	var bin, overrideRoot string
	var err error
	stderr := captureStderr(t, func() {
		bin, overrideRoot, err = resolvePluginBinary(context.Background(), srcDir, "plugin-h4", repo)
	})
	if err != nil {
		t.Fatalf("the override arm must build from the overridden source: %v", err)
	}
	if len(loader.asked) != 1 || loader.asked[0] != repo {
		t.Fatalf("the seam must be asked EXACTLY about the plugin's repo: asked=%v want [%s]", loader.asked, repo)
	}
	if overrideRoot != root {
		t.Fatalf("resolvePluginBinary override root = %q, want %q", overrideRoot, root)
	}
	if bin == bakedBin {
		t.Fatalf("the BAKED binary %s served an overridden repo — the override must outrank it", bakedBin)
	}
	if wantDir := filepath.Join(cache, "charly", "plugins"); filepath.Dir(bin) != wantDir {
		t.Fatalf("the override arm must host-build into the plugin cache %s; got %s", wantDir, bin)
	}
	if !strings.Contains(stderr, "ignoring baked binary "+bakedBin) || !strings.Contains(stderr, "CHARLY_REPO_OVERRIDE root "+root+" is authoritative") {
		t.Fatalf("the bypassed baked binary must be named LOUDLY; stderr:\n%s", stderr)
	}
	line := captureStderr(t, func() { reportPluginServed("plugin-h4", srcDir, bin, overrideRoot) })
	want := "plugin plugin-h4: using LOCAL OVERRIDE " + root + " — serving " + bin
	if !strings.Contains(line, want) {
		t.Fatalf("provenance line %q is missing %q", line, want)
	}
	if !strings.Contains(line, "source "+srcDir) {
		t.Fatalf("the override line must name the tree the build ran from; stderr:\n%s", line)
	}
}

// TestResolvePluginBinary_NoOverrideLeavesTheBakedPathIntact is the CONTROL: the seam IS asked
// (the repo is known), answers "no override", and the baked binary serves exactly as before.
func TestResolvePluginBinary_NoOverrideLeavesTheBakedPathIntact(t *testing.T) {
	bakedDir := t.TempDir()
	bakedBin := filepath.Join(bakedDir, "plugin-h4")
	if err := os.WriteFile(bakedBin, []byte("prebuilt"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeProviders(t, bakedDir, "plugin-h4", "verb:h4")
	isolatePluginSearch(t, bakedDir)

	const repo = "github.com/opencharly/plugin-h4"
	loader := &fakeOverrideLoader{found: false}
	swapProjectLoader(t, loader)

	bin, overrideRoot, err := resolvePluginBinary(context.Background(), t.TempDir(), "plugin-h4", repo)
	if err != nil || bin != bakedBin || overrideRoot != "" {
		t.Fatalf("with no override the baked binary must serve unchanged; got (%q,%q,%v), want (%q,\"\",nil)", bin, overrideRoot, err, bakedBin)
	}
	if len(loader.asked) != 1 || loader.asked[0] != repo {
		t.Fatalf("the seam must still be asked about the repo: asked=%v", loader.asked)
	}
	line := captureStderr(t, func() { reportPluginServed("plugin-h4", "", bin, overrideRoot) })
	if !strings.Contains(line, "served from baked binary "+bakedBin) {
		t.Fatalf("the no-override line must stay the baked line; stderr:\n%s", line)
	}
}

// TestResolvePluginBinary_OverrideSeamErrorFailsLoud: a malformed entry / missing override target
// is a hard error from the seam (loaderkit's fail-loud verdict); the resolver must propagate it
// rather than fall through to a baked binary — a typo must never silently serve other bytes.
func TestResolvePluginBinary_OverrideSeamErrorFailsLoud(t *testing.T) {
	bakedDir := t.TempDir()
	bakedBin := filepath.Join(bakedDir, "plugin-h4")
	if err := os.WriteFile(bakedBin, []byte("prebuilt"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeProviders(t, bakedDir, "plugin-h4", "verb:h4")
	isolatePluginSearch(t, bakedDir)

	boom := errors.New("CHARLY_REPO_OVERRIDE: malformed entry \"broken\" (want repoPath=localDir)")
	swapProjectLoader(t, &fakeOverrideLoader{err: boom})

	bin, overrideRoot, err := resolvePluginBinary(context.Background(), t.TempDir(), "plugin-h4", "github.com/opencharly/plugin-h4")
	if !errors.Is(err, boom) {
		t.Fatalf("the seam error must propagate; got (%q,%q,%v)", bin, overrideRoot, err)
	}
	if bin != "" || overrideRoot != "" {
		t.Fatalf("a failed override question must not resolve a binary; got (%q,%q)", bin, overrideRoot)
	}
}

// TestResolvePluginBinary_RealSeamServesTheOverrideTree drives the REAL compiled-in loader
// (TestMain loads it) with a real CHARLY_REPO_OVERRIDE value, so the answer provably comes from
// loaderkit's single parse through spec.ProjectLoader.RepoOverrideDir: the SHORT repo form is
// normalized (opencharly/plugin-h4 → github.com/opencharly/plugin-h4), a repo with no entry is
// NOT reported as overridden, and a missing override target fails loud through the real parse.
func TestResolvePluginBinary_RealSeamServesTheOverrideTree(t *testing.T) {
	if testing.Short() {
		t.Skip("the override arm host-builds a real module")
	}
	cache := t.TempDir()
	isolatePluginSearch(t, "")
	t.Setenv("XDG_CACHE_HOME", cache)

	root := t.TempDir()
	srcDir := writeMinimalPluginModule(t, root, "plugin-h4")
	t.Setenv(proc.RepoOverrideEnv, "opencharly/plugin-h4="+root)

	bin, overrideRoot, err := resolvePluginBinary(context.Background(), srcDir, "plugin-h4", "github.com/opencharly/plugin-h4")
	if err != nil {
		t.Fatalf("the real seam must resolve the override: %v", err)
	}
	if overrideRoot != root {
		t.Fatalf("real-seam override root = %q, want %q (the short-form entry must normalize)", overrideRoot, root)
	}
	if wantDir := filepath.Join(cache, "charly", "plugins"); filepath.Dir(bin) != wantDir {
		t.Fatalf("the overridden source must be host-built into %s; got %s", wantDir, bin)
	}
	if root2, ok, err := repoOverrideRootFor("github.com/opencharly/other"); err != nil || ok {
		t.Fatalf("a repo with NO entry must not be reported as overridden; got (%q,%v,%v)", root2, ok, err)
	}
	t.Setenv(proc.RepoOverrideEnv, "opencharly/plugin-h4=/nonexistent-override-tree-charly-h4")
	if _, _, err := resolvePluginBinary(context.Background(), srcDir, "plugin-h4", "github.com/opencharly/plugin-h4"); err == nil {
		t.Fatal("a missing override target must fail loud, not fall back to the baked/source path")
	}
}

// TestRepoOverrideRootFor_SkipsTheSeamForARePOlessSource pins the short-circuit: a plugin whose
// source names no remote repo never consults the seam (the nil-interface fake would panic if it
// did), so a local candy dir's plugins are untouched by an override setting.
func TestRepoOverrideRootFor_SkipsTheSeamForARepolessSource(t *testing.T) {
	loader := &fakeOverrideLoader{}
	swapProjectLoader(t, loader)
	if root, ok, err := repoOverrideRootFor(""); root != "" || ok || err != nil {
		t.Fatalf("repoOverrideRootFor(\"\") = (%q,%v,%v), want the no-question default", root, ok, err)
	}
	if len(loader.asked) != 0 {
		t.Fatalf("the seam must NOT be asked for a repo-less plugin; asked=%v", loader.asked)
	}
}

// writeCommandPluginProject writes a MINIMAL real project whose candy declares command:<word> with
// the given source: ref — the declaration resolveCommandPluginBinary scans for the plugin's repo,
// and (the candy dir being a buildable Go module) the source the override arm builds.
func writeCommandPluginProject(t *testing.T, root, name, word, source string) string {
	t.Helper()
	srcDir := writeMinimalPluginModule(t, root, name)
	candyYAML := name + ":\n    candy:\n        version: 2026.175.0001\n        description: a command-plugin fixture candy.\n        plugin:\n            providers:\n                - command:" + word + "\n            source: " + source + "\n        plan:\n            - check: command=true\n              id: " + name + "-check\n              context:\n                  - build\n              command: \"true\"\n"
	if err := os.WriteFile(filepath.Join(srcDir, "charly.yml"), []byte(candyYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	rootYAML := "version: " + LatestSchemaVersion().String() + "\ndiscover:\n    - path: candy\n      recursive: true\n"
	if err := os.WriteFile(filepath.Join(root, "charly.yml"), []byte(rootYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	return srcDir
}

// TestResolveCommandPluginBinary_OverrideOutranksTheBakedManifest closes the arm the baked-only
// COMMAND shortcut left open (#592 block 1): `charly <plugin-command>` returned a baked binary for
// the word BEFORE any override question was asked, so a local CHARLY_REPO_OVERRIDE for that
// plugin's repo did not outrank it — the PR's own claimed precedence (LOCAL OVERRIDE > baked
// binary > host build) did not hold on the command path. A command word's repo is in NO manifest
// (the .providers file records class:word), so the project scan is what names it, and the baked hit
// is now taken only once the seam has answered "no override" for THAT repo.
//
// It drives the REAL seam (the compiled-in loader TestMain loads) with a real env value: the
// override LHS is the candy's own source: declaration in the SHORT form (owner/repo), so the match
// itself proves the short form reaches the seam as github.com/owner/repo (#592 block 3).
func TestResolveCommandPluginBinary_OverrideOutranksTheBakedManifest(t *testing.T) {
	if testing.Short() {
		t.Skip("the override arm host-builds a real module")
	}
	const word = "zzoverridecmd"
	const candyName = "plugin-zzoverridecmd"
	cache := t.TempDir()
	bakedDir := t.TempDir()
	isolatePluginSearch(t, bakedDir)
	t.Setenv("XDG_CACHE_HOME", cache)
	bakedBin := filepath.Join(bakedDir, candyName)
	if err := os.WriteFile(bakedBin, []byte("prebuilt"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeProviders(t, bakedDir, candyName, "command:"+word)
	discoverBakedPluginWords() // the REAL .providers → class:word wiring main runs at startup
	defer delete(bakedPluginBinaries, provKey(ClassCommand, word))
	if got := bakedPluginBinaries[provKey(ClassCommand, word)]; got != bakedBin {
		t.Fatalf("the baked manifest hit = %q, want %q", got, bakedBin)
	}

	project := t.TempDir()
	writeCommandPluginProject(t, project, candyName, word, "opencharly/"+candyName)
	t.Chdir(project)

	// The override LHS is the SHORT form of the candy's own source: ref — it can only match if the
	// derivation normalized it to github.com/opencharly/<candy> the way the seam's parse does.
	root := t.TempDir()
	t.Setenv(proc.RepoOverrideEnv, "opencharly/"+candyName+"="+root)

	var bin string
	var err error
	stderr := captureStderr(t, func() { bin, err = resolveCommandPluginBinary(context.Background(), word) })
	if err != nil {
		t.Fatalf("resolveCommandPluginBinary with an override for the plugin's repo: %v", err)
	}
	if bin == bakedBin {
		t.Fatalf("the BAKED manifest hit %s served an overridden repo — the override must outrank it", bakedBin)
	}
	if wantDir := filepath.Join(cache, "charly", "plugins"); filepath.Dir(bin) != wantDir {
		t.Fatalf("the override arm must host-build into the plugin cache %s; got %s", wantDir, bin)
	}
	if !strings.Contains(stderr, "ignoring baked binary "+bakedBin) {
		t.Fatalf("the bypassed baked binary must be named LOUDLY; stderr:\n%s", stderr)
	}
	if !strings.Contains(stderr, "using LOCAL OVERRIDE "+root) {
		t.Fatalf("the command path must log the override provenance (the exec'd plugin IS the process); stderr:\n%s", stderr)
	}

	// CONTROL: no override → the baked manifest hit serves, unchanged (the deployed-container path
	// the shortcut exists for) — and this time with the candy DECLARED in the project, so the scan
	// really did run and the seam really did answer "no".
	t.Setenv(proc.RepoOverrideEnv, "")
	bin2, err2 := resolveCommandPluginBinary(context.Background(), word)
	if err2 != nil || bin2 != bakedBin {
		t.Fatalf("with no override the baked shortcut must serve unchanged; got (%q,%v), want %q", bin2, err2, bakedBin)
	}
}

// TestResolveCommandPluginBinary_BakedServesWithNoProjectHit is the CONTROL for the arm the
// shortcut exists for — the deployed container: `.providers` maps the word to a baked binary and
// the cwd is NOT a project, so no repo is derivable, the seam is never asked, and the baked binary
// serves byte-identically to the pre-fix behavior.
func TestResolveCommandPluginBinary_BakedServesWithNoProjectHit(t *testing.T) {
	const word = "zzbakednoproj"
	bakedDir := t.TempDir()
	isolatePluginSearch(t, bakedDir)
	bakedBin := filepath.Join(bakedDir, "plugin-zzbakednoproj")
	if err := os.WriteFile(bakedBin, []byte("prebuilt"), 0o755); err != nil {
		t.Fatal(err)
	}
	bakedPluginBinaries[provKey(ClassCommand, word)] = bakedBin
	defer delete(bakedPluginBinaries, provKey(ClassCommand, word))

	// A project-less cwd: NO repo is derivable, so the seam has nothing to be asked about and the
	// baked shortcut is the whole answer (the deployed-container path).
	t.Chdir(t.TempDir())

	bin, err := resolveCommandPluginBinary(context.Background(), word)
	if err != nil || bin != bakedBin {
		t.Fatalf("a project-less baked command must serve unchanged; got (%q,%v), want %q", bin, err, bakedBin)
	}
}
