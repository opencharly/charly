package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestPluginsGenReproducible is the drift gate for the committed generated plugin files: it
// regenerates plugins_generated.go + go.work + plugins_refs_generated.go from charly.yml's
// `compiled_plugins:` plus the org-wide corpus (charly/plugin_corpus.txt) and asserts the
// committed files match byte-for-byte. It fails if someone hand-edits a generated file, or
// changes compiled_plugins / the corpus without re-running `scripts/bootstrap-charly.sh`
// (which runs pluginsgen). Mirrors spec.TestGenReproducible for the CUE-gen path.
func TestPluginsGenReproducible(t *testing.T) {
	root := filepath.Join("..", "..", "..") // charly/internal/pluginsgen -> repo root
	genGo, genWork, genDevWork, genRefs, err := generate(root, filepath.Join("charly", "charly.yml"), filepath.Join("charly", "plugin_corpus.txt"), nil)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	// A plain generation must emit NO dev workspace: the tree gains nothing, and a later build
	// cannot pick one up by accident (writeDevWork deletes any stale one on this path).
	if genDevWork != nil {
		t.Errorf("a plain generation produced a dev workspace (%d bytes) — it must exist only for a -dev-plugin build", len(genDevWork))
	}
	for _, tc := range []struct {
		rel string
		got []byte
	}{
		{filepath.Join("charly", "plugins_generated.go"), genGo},
		{"go.work", genWork},
		{filepath.Join("charly", "plugins_refs_generated.go"), genRefs},
	} {
		committed, err := os.ReadFile(filepath.Join(root, tc.rel))
		if err != nil {
			t.Fatalf("read committed %s: %v", tc.rel, err)
		}
		if string(committed) != string(tc.got) {
			t.Errorf("%s is stale — re-run `scripts/bootstrap-charly.sh` (pluginsgen) and commit it.\n--- committed ---\n%s\n--- regenerated ---\n%s",
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
		"charly.yml": "my-plugin:\n\n    candy:\n        plugin:\n            providers:\n                - command:dsh\nmy-plugin-cli-skill:\n    skill:\n        name: my-plugin-cli-skill\n",
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

// TestDevPluginOverrideReadsTheLocalTree is the regression test for opencharly/charly#775: a
// -dev-plugin override must build a compiled-in plugin FROM THE LOCAL CHECKOUT — the module the
// dev workspace `use`s, the word->ref index, and the plugin-block/shape reads, all off that tree.
//
// It is hermetic, and that is what makes it a proof rather than a claim: the temp root holds no
// other copy of the plugin and the corpus is empty, so a word that exists ONLY in the local
// candy's charly.yml can appear in the generated index only if the generator read the local tree.
// (Before the override there was no local read at all: every read went to the module-proxy tag.)
func TestDevPluginOverrideReadsTheLocalTree(t *testing.T) {
	root := t.TempDir()
	writeCandy(t, root, map[string]string{
		"charly/go.mod": "module github.com/opencharly/charly\n\ngo 1.26.4\n\n" +
			"require (\n\tgithub.com/opencharly/plugin-fake/candy/plugin-fake v0.2026001.1200\n)\n",
		"charly/charly.yml": "compiled_plugins:\n    - plugin-fake\n",
	})
	local := filepath.Join(root, "local", "plugin-fake")
	writeCandy(t, local, map[string]string{
		"candy/plugin-fake/go.mod":     "module github.com/opencharly/plugin-fake/candy/plugin-fake\n\ngo 1.26.4\n",
		"candy/plugin-fake/main.go":    "package main\n\nfunc NewProvider() {}\n",
		"candy/plugin-fake/charly.yml": "plugin-fake:\n    candy:\n        plugin:\n            providers:\n                - check:local-only-word\n            source: github.com/opencharly/plugin-fake/candy/plugin-fake\n",
	})

	genGo, genWork, genDevWork, genRefs, err := generate(root, "charly/charly.yml", "", []devPlugin{{name: "plugin-fake", dir: local}})
	if err != nil {
		t.Fatalf("generate with a dev override: %v", err)
	}
	if want := "github.com/opencharly/plugin-fake/candy/plugin-fake"; !strings.Contains(string(genGo), want) {
		t.Fatalf("plugins_generated.go does not import %s:\n%s", want, genGo)
	}
	if want := `"check:local-only-word"`; !strings.Contains(string(genRefs), want) {
		t.Fatalf("the word->ref index does not carry %s — the index did not read the LOCAL tree:\n%s", want, genRefs)
	}
	// The committed workspace stays override-free: the build resolves the local module through
	// the DEV workspace only, so a plain build cannot inherit the override.
	if strings.Contains(string(genWork), "plugin-fake") {
		t.Fatalf("go.work names the dev override — the committed workspace must stay override-free:\n%s", genWork)
	}
	wantUse := "use " + filepath.Join(local, "candy", "plugin-fake")
	if !strings.Contains(string(genDevWork), wantUse) {
		t.Fatalf("go.work.dev does not carry %q:\n%s", wantUse, genDevWork)
	}
	if !strings.Contains(string(genDevWork), "use ./charly") {
		t.Fatalf("go.work.dev does not keep the charly module (the build runs from charly/):\n%s", genDevWork)
	}
}

// TestDevWorkspaceAbsentWithoutAnOverride pins the other half of the guarantee: a build with no
// override produces NO dev workspace, so nothing new appears in the tree and nothing can resolve
// a local checkout by accident.
func TestDevWorkspaceAbsentWithoutAnOverride(t *testing.T) {
	if got := devWorkspace("1.26.4", nil); got != nil {
		t.Fatalf("devWorkspace with no override = %q, want nil", got)
	}
}

// TestDevWorkspaceQuotesAPathThatNeedsIt pins the one place the generated workspace could become a
// syntax error instead of a build error: a `use` path is a single whitespace-delimited token, and a
// dev override is an ARBITRARY local path — one under a directory with a space is ordinary, not
// exotic. Go's own writer quotes such a path (modfile.AutoQuote, which `go work use` writes with);
// this asserts the generator follows that rule, and only when it is needed.
func TestDevWorkspaceQuotesAPathThatNeedsIt(t *testing.T) {
	withSpace := filepath.Join(t.TempDir(), "a checkout")
	got := string(devWorkspace("1.26.4", map[string]string{"plugin-fake": withSpace}))
	want := "use " + strconv.Quote(filepath.Join(withSpace, "candy", "plugin-fake")) + "\n"
	if !strings.Contains(got, want) {
		t.Fatalf("go.work.dev does not carry %q:\n%s", want, got)
	}

	plain := filepath.Join(t.TempDir(), "checkout")
	got = string(devWorkspace("1.26.4", map[string]string{"plugin-fake": plain}))
	want = "use " + filepath.Join(plain, "candy", "plugin-fake") + "\n"
	if !strings.Contains(got, want) {
		t.Fatalf("go.work.dev did not carry the unquoted %q:\n%s", want, got)
	}
}

// TestResolveDevPluginsRejectsANameThatIsNotCompiledIn: only a plugin this build COMPILES IN can be
// re-pointed at a local checkout, so naming anything else must fail at the flag rather than
// silently do nothing (the module would not even be linked into the binary).
func TestResolveDevPluginsRejectsANameThatIsNotCompiledIn(t *testing.T) {
	_, err := resolveDevPlugins([]string{"plugin-fake"}, "charly/charly.yml", []devPlugin{{name: "plugin-other", dir: t.TempDir()}})
	if err == nil {
		t.Fatal("resolveDevPlugins accepted a name that is not in compiled_plugins:")
	}
	if !strings.Contains(err.Error(), "not in the compiled_plugins: list of charly/charly.yml") {
		t.Fatalf("resolveDevPlugins err = %v, want it to name compiled_plugins: and the config file", err)
	}
}

// TestResolveDevPluginsRejectsACheckoutThatIsNotThatPlugin pins the second check: the generated
// registration imports github.com/opencharly/<name>/candy/<name>, so a checkout whose candy
// declares any other module path cannot satisfy that import. Failing here names the flag, the
// go.mod and both paths — instead of leaving a go build to fail with none of them.
func TestResolveDevPluginsRejectsACheckoutThatIsNotThatPlugin(t *testing.T) {
	dir := t.TempDir()
	writeCandy(t, dir, map[string]string{
		"candy/plugin-fake/go.mod": "module github.com/opencharly/plugin-other/candy/plugin-fake\n\ngo 1.26.4\n",
	})
	_, err := resolveDevPlugins([]string{"plugin-fake"}, "charly/charly.yml", []devPlugin{{name: "plugin-fake", dir: dir}})
	if err == nil {
		t.Fatal("resolveDevPlugins accepted a checkout whose module path is not the compiled-in plugin's")
	}
	if !strings.Contains(err.Error(), "plugin-other") {
		t.Fatalf("resolveDevPlugins err = %v, want it to name the module path it actually found", err)
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
