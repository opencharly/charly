package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// generatedWiring caches ONE pluginsgen run for the two gates below. It is cached, not
// shared state for its own sake: building the refs index fetches every corpus plugin at its
// default branch (112 repos, ~100 s), and running that once per gate would double a live
// cost that carries no extra signal.
var (
	generatedOnce    sync.Once
	generatedGo      []byte
	generatedWork    []byte
	generatedDevWork []byte
	generatedErr     error
)

// generatedPluginWiring returns the generated wiring for the repository's own tree. The
// error is stored by the once and reported by the caller, never by the once body: a t.Fatal
// inside sync.Once would blame whichever gate happened to run first and skip the other.
func generatedPluginWiring(t *testing.T) (genGo, genWork []byte) {
	t.Helper()
	generatedOnce.Do(func() {
		root := filepath.Join("..", "..", "..") // charly/internal/pluginsgen -> repo root
		generatedGo, generatedWork, generatedDevWork, _, generatedErr =
			generate(root, filepath.Join("charly", "charly.yml"), filepath.Join("charly", "plugin_corpus.txt"), nil)
	})
	if generatedErr != nil {
		t.Fatalf("generate: %v", generatedErr)
	}
	// A plain generation must emit NO dev workspace: the tree gains nothing, and a later build
	// cannot pick one up by accident (writeDevWork deletes any stale one on this path).
	if generatedDevWork != nil {
		t.Errorf("a plain generation produced a dev workspace (%d bytes) — it must exist only for a -dev-plugin build", len(generatedDevWork))
	}
	return generatedGo, generatedWork
}

// TestPluginsGenReproducible is the drift gate for the committed generated plugin files that
// CAN be reproduced from TRACKED inputs: plugins_generated.go (from charly.yml's
// `compiled_plugins:` + the go.mod pins, read at their immutable tags) and go.work (from the
// go.mod go directive). It asserts those two match byte-for-byte, so it fails if someone
// hand-edits one, or changes compiled_plugins without re-running
// `scripts/bootstrap-charly.sh` (which runs pluginsgen). Mirrors spec.TestGenReproducible for
// the CUE-gen path.
//
// charly/plugins_refs_generated.go is deliberately NOT byte-compared here — it is a
// projection of the corpus's DEFAULT BRANCHES, so byte-equality with a live re-fetch is a
// contract that cannot hold and reddened `main` on upstream plugin commits alone
// (opencharly/charly#792). TestPluginsRefsIndexSound gates it on a well-defined invariant
// instead.
func TestPluginsGenReproducible(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	genGo, genWork := generatedPluginWiring(t)
	for _, tc := range []struct {
		rel string
		got []byte
	}{
		{filepath.Join("charly", "plugins_generated.go"), genGo},
		{"go.work", genWork},
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

// TestPluginsRefsIndexSound gates the committed word->ref index on the only invariant that
// is TRUE of a projection of a moving upstream: every word it names must STILL be served by
// that same ref.
//
//   - a word the corpus no longer serves, or serves from a different ref, means the
//     committed index is FALSE — it sends a reader to a plugin that does not provide the
//     word. That is a real breakage, so it FAILS.
//   - a word the corpus serves that the index does not name is an upstream ADDITION: the
//     index is incomplete, not wrong, and nobody in this repository can make it complete
//     except by regenerating. It is reported as an advisory and never fails, which is what
//     keeps an upstream plugin commit from reddening `main` on its own schedule.
//
// The limit, stated plainly rather than papered over: a hand-edit that DELETES a row is
// indistinguishable from an upstream addition when all you have is the committed file —
// both leave the word absent from the committed index and present in the live one — so a
// deletion lands on the advisory path too. Telling those apart needs the corpus commit
// recorded alongside each entry (a pinned corpus: the other option, and a separate
// decision). What this gate catches reliably is a FALSE index: a repointed provider, and a
// provider the corpus has dropped — both verified to fail by tampering the committed file.
//
// The committed side is parsed as SOURCE rather than re-derived: the gate compares what is
// committed against what the corpus serves — re-deriving it would compare a value to itself.
func TestPluginsRefsIndexSound(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	names, err := readCompiledPlugins(filepath.Join(root, "charly", "charly.yml"))
	if err != nil {
		t.Fatalf("readCompiledPlugins: %v", err)
	}
	live, err := collectWordRefs(names, corpusRepoList(root, filepath.Join("charly", "plugin_corpus.txt"), names), nil)
	if err != nil {
		t.Fatalf("collectWordRefs: %v", err)
	}
	if len(live) == 0 {
		t.Fatal("the live index is empty — the corpus fetch, not the index, is broken")
	}
	committed := readCommittedProviderRefs(t, filepath.Join(root, "charly", "plugins_refs_generated.go"))

	var lost, repointed []string
	for word, ref := range committed {
		got, ok := live[word]
		switch {
		case !ok:
			lost = append(lost, word)
		case got != ref:
			repointed = append(repointed, word+" is committed as "+ref+" but the corpus now serves it from "+got)
		}
	}
	sort.Strings(lost)
	sort.Strings(repointed)
	if len(lost) > 0 {
		t.Errorf("the committed index names %d word(s) no corpus plugin serves any more — the index is FALSE; regenerate and commit it:\n  %s",
			len(lost), strings.Join(lost, "\n  "))
	}
	if len(repointed) > 0 {
		t.Errorf("the committed index points %d word(s) at the wrong provider:\n  %s",
			len(repointed), strings.Join(repointed, "\n  "))
	}

	var added []string
	for word := range live {
		if _, ok := committed[word]; !ok {
			added = append(added, word)
		}
	}
	if len(added) > 0 {
		sort.Strings(added)
		t.Logf("ADVISORY (not a failure): the corpus serves %d word(s) the committed index does not name — it is incomplete, not wrong. Regenerate with `scripts/bootstrap-charly.sh` to pick them up:\n  %s",
			len(added), strings.Join(added, "\n  "))
	}
}

// readCommittedProviderRefs parses the committed provider-ref index into a map, so the gate
// above can compare it against what the corpus serves right now.
func readCommittedProviderRefs(t *testing.T, path string) map[string]string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	out := map[string]string{}
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range vs.Names {
				if name.Name != "pluginProviderRefs" || i >= len(vs.Values) {
					continue
				}
				lit, ok := vs.Values[i].(*ast.CompositeLit)
				if !ok {
					t.Fatalf("%s: pluginProviderRefs is not a composite literal — the index's shape changed", path)
				}
				for _, elt := range lit.Elts {
					kv, ok := elt.(*ast.KeyValueExpr)
					if !ok {
						t.Fatalf("%s: unexpected entry in pluginProviderRefs", path)
					}
					key, val, ok := stringPair(kv)
					if !ok {
						t.Fatalf("%s: pluginProviderRefs carries a non-literal entry — the index's shape changed", path)
					}
					out[key] = val
				}
			}
		}
	}
	if len(out) == 0 {
		t.Fatalf("%s: parsed no entries — the parse is broken, not the index", path)
	}
	return out
}

// stringPair unquotes a `"key": "value"` entry, reporting false for any other shape.
func stringPair(kv *ast.KeyValueExpr) (key, val string, ok bool) {
	k, kOK := kv.Key.(*ast.BasicLit)
	v, vOK := kv.Value.(*ast.BasicLit)
	if !kOK || !vOK || k.Kind != token.STRING || v.Kind != token.STRING {
		return "", "", false
	}
	key, err := strconv.Unquote(k.Value)
	if err != nil {
		return "", "", false
	}
	val, err = strconv.Unquote(v.Value)
	if err != nil {
		return "", "", false
	}
	return key, val, true
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
