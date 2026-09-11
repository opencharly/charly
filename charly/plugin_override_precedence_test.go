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
// source that names no remote repo (a local candy dir, a bare name) yields "": nothing for an
// override to match, and the seam is never asked.
func TestPluginRepoPath_NamesTheRepoOfAPluginSource(t *testing.T) {
	for _, tc := range []struct{ source, want string }{
		{"github.com/opencharly/plugin-h4/candy/plugin-h4", "github.com/opencharly/plugin-h4"},
		{"@github.com/opencharly/plugin-h4/candy/plugin-h4:v2026.237.1417", "github.com/opencharly/plugin-h4"},
		{"candy/plugin-local", ""},
		{"./candy/plugin-local", ""},
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
