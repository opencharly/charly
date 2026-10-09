// Command pluginsgen emits the compiled-in plugin wiring from a charly.yml's
// `compiled_plugins:` list: charly/plugins_generated.go (one registerCompiledPlugin
// call per plugin candy) AND the repo-root go.work (a `use` directive per candy, so
// `go build ./charly` resolves the candy imports in plugins_generated.go).
//
// It is the in-proc-placement counterpart of the out-of-process plugin loader:
// `compiled_plugins:` selects which plugin candies are COMPILED INTO this charly
// binary; everything else still loads out-of-process over gRPC when referenced (the
// coexist path). "which plugins are in the binary" is therefore a normal
// candy-inclusion choice, expressed in charly.yml.
//
// It imports only the stdlib + gopkg.in/yaml.v3 + spec/refs (the contract module's
// standalone fetch) and NEVER imports the candy modules (it string-templates their
// import paths from each compiled plugin's module path — github.com/opencharly/<name>/
// candy/<name>, the standalone convention), so it builds and runs without a pre-built
// charly and without the candy modules resolving — which is what lets `scripts/bootstrap-charly.sh`
// run it BEFORE the `go build` that needs go.work (dodging the chicken-and-egg). Run it
// with GOWORK=off so a stale go.work can't fail workspace load before regeneration.
//
// STANDALONE SHAPE (the candy de-submodule cutover, Phase 4): the compiled-in plugin
// candies live in their own repos (opencharly/<name>), fetched at the go.mod-pinned
// version via spec/refs.DownloadRepo — the same standalone fetch the runtime uses. The
// plugin-block check and the kit/pb shape detection read the FETCHED repo's
// candy/<name>/charly.yml + Go source (the in-repo candy/ dirs are deleted).
//
// DEV-TREE OVERRIDE (opencharly/charly#775): `-dev-plugin <candy-name>=<repo-checkout>`
// (repeatable) builds a COMPILED-IN plugin from a LOCAL checkout instead of its pinned
// module-proxy tag, and emits the workspace that resolves it (go.work.dev, selected by an
// explicit GOWORK — never auto-detected) so a bed run on that binary exercises UNMERGED plugin
// source. Without an override nothing changes: the three committed artifacts stay byte-identical
// and no dev workspace exists.
//
// Reproducible: same `compiled_plugins:` list -> byte-identical outputs (guarded by
// TestPluginsGenReproducible). Run from `scripts/bootstrap-charly.sh`; edit charly.yml's
// compiled_plugins and re-run, never the generated files.
package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"go/format"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/opencharly/spec/refs"
	"gopkg.in/yaml.v3"
)

func main() {
	cfg := flag.String("config", "charly/charly.yml", "charly.yml whose compiled_plugins: drives generation (relative to -root)")
	root := flag.String("root", ".", "repo root (contains charly/ and candy/); go.work use-paths are relative to it")
	outGo := flag.String("out", "charly/plugins_generated.go", "generated registration file (relative to -root)")
	outWork := flag.String("gowork", "go.work", "generated go.work (relative to -root)")
	outRefs := flag.String("outrefs", "charly/plugins_refs_generated.go", "generated word->candy-ref index (relative to -root)")
	outDevWork := flag.String("gowork-dev", "go.work.dev", "dev workspace emitted when -dev-plugin is given (relative to -root); removed when it is not, so a stale one can never resolve a local tree for a later build")
	corpus := flag.String("corpus", "", "optional newline list of plugin repo paths (relative to -root) whose manifests are indexed; the org-wide plugin-* set. Empty => only the compiled_plugins corpus is indexed.")
	var devs devPluginFlag
	flag.Var(&devs, "dev-plugin", "build a compiled-in plugin from a LOCAL checkout instead of its pinned tag: <candy-name>=<repo-checkout> (repeatable). Dev builds only — see the package doc.")
	flag.Parse()

	if err := run(*root, *cfg, *outGo, *outWork, *outDevWork, *outRefs, *corpus, devs); err != nil {
		fmt.Fprintf(os.Stderr, "pluginsgen: %v\n", err)
		os.Exit(1)
	}
}

// devPlugin is one -dev-plugin override: the candy name of a compiled-in plugin, and the local
// checkout of that plugin's repo (the candy module is <dir>/candy/<name>).
type devPlugin struct {
	name string
	dir  string
}

// devPluginFlag collects the repeatable -dev-plugin flags.
type devPluginFlag []devPlugin

func (f *devPluginFlag) String() string {
	specs := make([]string, 0, len(*f))
	for _, d := range *f {
		specs = append(specs, d.name+"="+d.dir)
	}
	return strings.Join(specs, ",")
}

// Set parses ONE <candy-name>=<repo-checkout>. The split is on the FIRST '=' so a checkout path
// may contain one; emptiness and the name's membership in compiled_plugins: are checked by
// resolveDevPlugins, which can name the file that list comes from.
func (f *devPluginFlag) Set(v string) error {
	name, dir, ok := strings.Cut(v, "=")
	if !ok {
		return fmt.Errorf("want <candy-name>=<repo-checkout>, got %q", v)
	}
	*f = append(*f, devPlugin{name: strings.TrimSpace(name), dir: strings.TrimSpace(dir)})
	return nil
}

func run(root, cfg, outGo, outWork, outDevWork, outRefs, corpus string, devs []devPlugin) error {
	genGo, genWork, genDevWork, genRefs, err := generate(root, cfg, corpus, devs)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, outGo), genGo, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", outGo, err)
	}
	if err := os.WriteFile(filepath.Join(root, outWork), genWork, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", outWork, err)
	}
	if err := os.WriteFile(filepath.Join(root, outRefs), genRefs, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", outRefs, err)
	}
	return writeDevWork(root, outDevWork, genDevWork)
}

// writeDevWork materializes the dev workspace — or, when no override is active (genDevWork nil),
// REMOVES it together with the checksum lock a previous dev build wrote beside it.
//
// The removal is the point: go.work.dev is selected by an explicit GOWORK= and never
// auto-detected, so a stale one left behind would keep resolving a local checkout for anyone who
// later built with that GOWORK — a build silently compiling unmerged plugin source while calling
// itself a plain build. The generator owns the file, so the generator deletes it the moment the
// override that justified it is gone.
func writeDevWork(root, rel string, body []byte) error {
	path := filepath.Join(root, rel)
	if body != nil {
		if err := os.WriteFile(path, body, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", rel, err)
		}
		return nil
	}
	for _, p := range []string{path, path + ".sum"} {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove stale %s: %w", p, err)
		}
	}
	return nil
}

// generate produces the byte content of plugins_generated.go + go.work from a
// charly.yml's compiled_plugins: list, WITHOUT writing — so the reproducibility gate
// (TestPluginsGenReproducible) can diff against the committed files.
// collectWordRefs builds the provider-ref INDEX ("<class>:<word>" -> canonical candy ref)
// by reading every plugin repo's OWN `plugin:` block — a pure PROJECTION of the plugin
// repos (boundary-law clause D); core keeps no hand-written word->ref map. An
// unavailable/out-of-tree corpus repo simply contributes no words, while a compiled-in one
// that cannot be indexed is a genuine build error.
//
// A word may be declared by BOTH a compiled-in plugin AND an external one — the by-design
// coexist the runtime per-word loader allows (charly#686): e.g. `kind:kubevirt` is the
// compiled-in plugin-substrate STRUCTURAL kind, re-declared by the external plugin-kubevirt
// alongside its OWN deploy/verb/command words. The compiled-in provider WINS the word; only
// TWO EXTERNAL providers for one word is a hard error (one canonical external provider per
// word).
func collectWordRefs(names, repoList []string, devRoots map[string]string) (map[string]string, error) {
	wordRefs := map[string]string{}
	for _, repo := range repoList {
		name, compiled := repoSetCompiled(names, repo)
		// A -dev-plugin override applies to the repo it names whether or not this build COMPILES
		// that plugin in: an out-of-process plugin is served from its own module at run time, and
		// the ref this index publishes is the thing that decides where the host looks. Keying it on
		// `compiled` was the whole of charly#835's limitation.
		//
		// ONE match per repo: a compiled plugin's override is keyed by the name just resolved, so it
		// is a map lookup; only a plugin this build does NOT compile in has to be matched against the
		// override set's own keys.
		repoRoot := devRoots[name]
		if repoRoot == "" && !compiled {
			repoRoot = devRepoRoot(devRoots, repo)
		}
		if repoRoot == "" {
			fetched, ferr := refs.DownloadRepo(repo, "HEAD")
			if ferr != nil {
				if compiled {
					return nil, fmt.Errorf("index plugin %s: %w", repo, ferr)
				}
				continue
			}
			repoRoot = fetched
		}
		refsForRepo, err := indexRepoPluginRefs(repoRoot)
		if err != nil {
			if compiled {
				return nil, fmt.Errorf("index plugin %s: %w", repo, err)
			}
			continue
		}
		for word, ref := range refsForRepo {
			prev, dup := wordRefs[word]
			if dup && prev != ref {
				_, prevCompiled := repoSetCompiled(names, prev)
				_, thisCompiled := repoSetCompiled(names, ref)
				if prevCompiled == thisCompiled {
					return nil, fmt.Errorf("provider word %s is served by two plugin refs (%s and %s) — a word has one canonical provider", word, prev, ref)
				}
				if thisCompiled {
					// The compiled-in provider owns the word; the external re-declaration
					// coexists (charly#686) and must not error.
					wordRefs[word] = ref
				}
				continue
			}
			wordRefs[word] = ref
		}
	}
	return wordRefs, nil
}

// writeDevOverrideSibling writes (or removes) charly/plugins_dev_generated.go — the UNTRACKED
// sibling that carries the VALUES for devRepoOverrideEntries. It is deliberately not part of the
// tracked wiring: a dev build must leave the tracked tree byte-clean, or the generated-clean guard
// refuses the build whose tracked generated file drifted (and it is right to).
func writeDevOverrideSibling(root string, envPairs []string) error {
	path := filepath.Join(root, "charly", "plugins_dev_generated.go")
	if len(envPairs) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	var b bytes.Buffer
	b.WriteString("// Code generated by charly/internal/pluginsgen. DO NOT EDIT.\n")
	b.WriteString("// UNTRACKED and gitignored: this file exists only in a -dev-plugin build of an OUT-OF-PROCESS\n")
	b.WriteString("// plugin, and carries the CHARLY_REPO_OVERRIDE entries that build was generated with.\n")
	b.WriteString("package main\n\nfunc init() {\n\tdevRepoOverrideEntries = []string{\n")
	for _, pair := range envPairs {
		fmt.Fprintf(&b, "\t\t%q,\n", pair)
	}
	b.WriteString("\t}\n}\n")
	formatted, err := format.Source(b.Bytes())
	if err != nil {
		return fmt.Errorf("format generated dev override sibling: %w\n%s", err, b.String())
	}
	return os.WriteFile(path, formatted, 0o644)
}

func generate(root, cfg, corpusFile string, devs []devPlugin) (genGo, genWork, genDevWork, genRefs []byte, err error) {
	names, err := readCompiledPlugins(filepath.Join(root, cfg))
	if err != nil {
		return nil, nil, nil, nil, err
	}
	// devRoots: candy name -> local plugin repo checkout, for the compiled-in plugins a
	// -dev-plugin override re-points. Empty (nil) on a plain build, which then takes every
	// path below exactly as it did before.
	devRoots, err := resolveDevPlugins(devs)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	// pluginRepos: the repos whose manifests are indexed — the compiled_plugins: corpus
	// PLUS any org-wide corpus (the umbrella's plugin-* set). Every repo is read for its OWN
	// `plugin:` blocks — the word->ref FACT lives only in each plugin's manifest, never in
	// charly.
	repoList := corpusRepoList(root, corpusFile, names)

	// wordRefs is the generated provider-ref INDEX ("<class>:<word>" -> canonical candy
	// ref), a pure PROJECTION of the plugin repos (boundary-law clause D): the core keeps
	// NO per-kind word->ref map of its own.
	wordRefs, err := collectWordRefs(names, repoList, devRoots)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	type entry struct {
		name   string // candy dir name under candy/
		module string // go.mod module path (== the importable root package)
		alias  string // import alias in the generated file
		shape  string // "kit" (NewCheckVerb, schema) or "pb" (NewProvider/NewMeta)
	}
	entries := make([]entry, 0, len(names))
	for _, name := range names {
		// The standalone module path is the convention: github.com/opencharly/<name>/candy/<name>.
		// The version comes from the charly module's go.mod require (the pinned Go tag — the
		// sdk/spec contract-module shape) and is required even under a -dev-plugin override: the
		// pin is the release contract, and the override is a dev deviation from it, never a
		// replacement for it (an unpinned compiled plugin is already invalid without one).
		mod := "github.com/opencharly/" + name + "/candy/" + name
		version, err := moduleVersion(filepath.Join(root, "charly", "go.mod"), mod)
		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf("compiled plugin %q: %w", name, err)
		}
		// candyDir is where the plugin's OWN tree is read from: the local checkout a dev
		// override names, else the repo fetched at the pinned tag via the SAME standalone fetch
		// the runtime uses (spec/refs.DownloadRepo). Only the SOURCE of the read differs — the
		// plugin-block check and the kit/pb shape detection below are identical either way.
		candyDir := ""
		if local, ok := devRoots[name]; ok {
			candyDir = filepath.Join(local, "candy", name)
		} else {
			// The Go tag is subdir-prefixed: candy/<name>/<version> (the module lives at
			// candy/<name> inside the repo — the convention the plugin-generate-packages
			// precedent set).
			repoPath := "github.com/opencharly/" + name
			fetched, err := refs.DownloadRepo(repoPath, "candy/"+name+"/"+version)
			if err != nil {
				return nil, nil, nil, nil, fmt.Errorf("compiled plugin %q: fetch %s@%s: %w", name, repoPath, version, err)
			}
			candyDir = filepath.Join(fetched, "candy", name)
		}
		if err := requirePluginBlock(filepath.Join(candyDir, "charly.yml")); err != nil {
			return nil, nil, nil, nil, fmt.Errorf("compiled plugin %q: %w", name, err)
		}
		shape, err := detectShape(candyDir)
		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf("compiled plugin %q: %w", name, err)
		}
		entries = append(entries, entry{name: name, module: mod, alias: goAlias(name), shape: shape})
	}

	// --- plugins_generated.go ---
	var g bytes.Buffer
	g.WriteString("// Code generated by charly/internal/pluginsgen (run by scripts/bootstrap-charly.sh). DO NOT EDIT.\n//\n")
	g.WriteString("// One registerCompiledPlugin() call per plugin candy named in charly.yml\n")
	g.WriteString("// `compiled_plugins:`. Regenerated by `scripts/bootstrap-charly.sh` before `go build`.\n")
	g.WriteString("// Each compiled-in plugin module is a `require` pin in charly/go.mod resolved\n")
	g.WriteString("// from the module proxy (no `use` directive in go.work — see the generator's\n")
	g.WriteString("// go.work writer); a dev build (-dev-plugin) resolves a plugin through go.work.dev\n")
	g.WriteString("// instead. Edit charly.yml's\n")
	g.WriteString("// compiled_plugins and re-run the generator, never this file.\n")
	g.WriteString("package main\n\n")
	if len(entries) > 0 {
		g.WriteString("import (\n")
		for _, e := range entries {
			fmt.Fprintf(&g, "\t%s %q\n", e.alias, e.module)
		}
		g.WriteString(")\n\n")
		g.WriteString("func init() {\n")
		for _, e := range entries {
			switch e.shape {
			case "kit":
				// kit-shape: host-coupled check verb, compiled-in-only — register through the
				// kit adapter, reading schema + input-defs from the candy's shared NewMeta
				// (the SAME Describe the pb shape below uses; no exported SchemaFS/InputDefs trio).
				fmt.Fprintf(&g, "\tregisterCompiledCheckVerb(%s.NewCheckVerb(), %s.NewMeta())\n", e.alias, e.alias)
			default:
				// pb-shape: dual-placement plugin — in-proc via the served Describe channel.
				fmt.Fprintf(&g, "\tregisterCompiledPlugin(%s.NewProvider(), %s.NewMeta())\n", e.alias, e.alias)
			}
		}
		g.WriteString("}\n")
	}
	// --- the OUT-OF-PROCESS dev overrides (opencharly/charly#835) ---
	// A COMPILED-IN plugin's -dev-plugin override rides go.work.dev, which the BUILD reads. An
	// out-of-process plugin is built by the HOST at run time, long after any build-time artefact is
	// out of scope — and the seam that serves it (spec.ProjectLoader.RepoOverrideDir, whose one
	// implementation lives in plugin-loader's own repo) cannot import anything generated here. The
	// env var that seam already parses is the only channel both sides share, so a dev build emits
	// its out-of-process overrides as entries for it; main seeds them, and an operator's own
	// CHARLY_REPO_OVERRIDE always wins.
	compiledSet := make(map[string]bool, len(names))
	for _, n := range names {
		compiledSet[n] = true
	}
	var envPairs []string
	for name, dir := range devRoots {
		if !compiledSet[name] {
			envPairs = append(envPairs, "github.com/opencharly/"+name+"="+dir)
		}
	}
	sort.Strings(envPairs)
	// The untracked dev sibling: the VALUES for devRepoOverrideEntries. Written (or removed) here,
	// beside the tracked artefacts, and gitignored, so a dev build leaves the tracked tree
	// byte-clean — which is exactly what the generated-clean guard exists to assert.
	if err := writeDevOverrideSibling(root, envPairs); err != nil {
		return nil, nil, nil, nil, err
	}
	// Declared, and EMPTY, on every build: the tracked artefact must not carry dev state, or the
	// bootstrap's generated-clean guard is right to refuse the build whose tracked generated file
	// drifted. The VALUES ride an untracked, gitignored sibling (written below and by main), so the
	// tracked file is byte-identical in a dev build and a plain one.
	g.WriteString("\n// devRepoOverrideEntries are this build's OUT-OF-PROCESS -dev-plugin overrides, as\n")
	g.WriteString("// CHARLY_REPO_OVERRIDE entries. Assigned by charly/plugins_dev_generated.go, which exists\n")
	g.WriteString("// only in a dev build and is gitignored. See main's seed.\n")
	g.WriteString("var devRepoOverrideEntries []string\n")

	formatted, err := format.Source(g.Bytes())
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("format generated go: %w\n%s", err, g.String())
	}

	// --- plugins_refs_generated.go — the word->candy-ref INDEX. A pure projection of the
	// plugin repos' own `plugin:` blocks (boundary-law clause D): the word->ref FACT lives
	// in each plugin repo, never in charly, and the core keeps no hand-written per-kind
	// map. Regenerated with the rest of the wiring; a new plugin needs its repo in the
	// corpus, no charly code change.
	var r bytes.Buffer
	r.WriteString("// Code generated by charly/internal/pluginsgen (run by scripts/bootstrap-charly.sh). DO NOT EDIT.\n//\n")
	r.WriteString("// pluginProviderRefs maps \"<class>:<word>\" -> the canonical candy ref of the plugin\n")
	r.WriteString("// that serves it, DERIVED from every plugin repo's own `plugin:` block (its\n")
	r.WriteString("// `providers:` + `source:`). This is the ONE word->provider fact: it lives in the\n")
	r.WriteString("// plugin repos, never in charly. Regenerate with the rest of the wiring.\n")
	r.WriteString("package main\n\n")
	r.WriteString("var pluginProviderRefs = map[string]string{\n")
	words := make([]string, 0, len(wordRefs))
	for w := range wordRefs {
		words = append(words, w)
	}
	sort.Strings(words)
	for _, w := range words {
		fmt.Fprintf(&r, "\t%q: %q,\n", w, wordRefs[w])
	}
	r.WriteString("}\n")
	formattedRefs, err := format.Source(r.Bytes())
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("format generated refs: %w\n%s", err, r.String())
	}

	// --- go.work ---
	goVer, err := readGoDirective(filepath.Join(root, "charly", "go.mod"))
	if err != nil {
		return nil, nil, nil, nil, err
	}
	var w bytes.Buffer
	fmt.Fprintf(&w, "go %s\n\n", goVer)
	w.WriteString("// Generated by charly/internal/pluginsgen (run by scripts/bootstrap-charly.sh). DO NOT EDIT.\n")
	w.WriteString("// The charly module plus the compiled-in plugin candies, resolved as PROXY\n")
	w.WriteString("// modules (the candy de-submodule cutover, Phase 4): every compiled-in plugin\n")
	w.WriteString("// module (github.com/opencharly/<name>/candy/<name>) is a `require` pin in\n")
	w.WriteString("// charly/go.mod at its Go tag, resolved from the module proxy — mirroring the\n")
	w.WriteString("// sdk/spec contract modules. There are no workspace members beyond charly; the\n")
	w.WriteString("// `use ./candy/...` in-repo shape was deleted with the in-repo candy dirs.\n")
	w.WriteString("use ./charly\n")

	// --- go.work.dev — the DEV WORKSPACE, emitted ONLY when an override is active ---
	return formatted, w.Bytes(), devWorkspace(goVer, devRoots), formattedRefs, nil
}

// resolveDevPlugins validates the -dev-plugin overrides against THIS build's compiled_plugins:
// list and returns candy name -> absolute plugin repo checkout, or nil when there are none.
//
// Both checks exist so a mistake fails HERE, naming the flag, instead of surfacing as a
// confusing error out of the `go build`:
//   - the named plugin must BE compiled in, since `compiled_plugins:` is the only set an override
//     can reach — only those modules are linked into the binary at all;
//   - the checkout must BE that plugin's module: the generated registration imports
//     github.com/opencharly/<name>/candy/<name> by the standalone convention, so a checkout whose
//     candy/<name>/go.mod declares any other module path cannot satisfy that import.
func resolveDevPlugins(devs []devPlugin) (map[string]string, error) {
	if len(devs) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(devs))
	for _, d := range devs {
		if d.name == "" || d.dir == "" {
			return nil, fmt.Errorf("-dev-plugin %q: want <candy-name>=<repo-checkout>", d.name+"="+d.dir)
		}
		if _, dup := out[d.name]; dup {
			return nil, fmt.Errorf("-dev-plugin %s: given twice", d.name)
		}
		abs, err := filepath.Abs(d.dir)
		if err != nil {
			return nil, fmt.Errorf("-dev-plugin %s: %s: %w", d.name, d.dir, err)
		}
		want := "github.com/opencharly/" + d.name + "/candy/" + d.name
		gomod := filepath.Join(abs, "candy", d.name, "go.mod")
		got, err := readModulePath(gomod)
		if err != nil {
			return nil, fmt.Errorf("-dev-plugin %s: %s: %w (the override must point at that plugin's repo checkout, whose candy/%s declares module %s)", d.name, gomod, err, d.name, want)
		}
		if got != want {
			return nil, fmt.Errorf("-dev-plugin %s: %s declares module %q, want %q — the generated registration imports the plugin by that path", d.name, gomod, got, want)
		}
		out[d.name] = abs
	}
	return out, nil
}

// devWorkspace renders the DEV WORKSPACE (go.work.dev) that a -dev-plugin build resolves with, or
// nil when no override is active.
//
// Why a SECOND workspace file and not a `use` line in go.work (opencharly/charly#775): go.work is
// a TRACKED artifact that every `scripts/bootstrap-charly.sh` regenerates, so a hand-added `use`
// is wiped by the next bootstrap — before the build that wanted it — and a local path could end
// up committed. A differently-named workspace is invisible to `go build`'s auto-detection (which
// looks for exactly `go.work`) and is selected only by an explicit GOWORK=; and Go keys a
// workspace's checksum lock off the WORKSPACE FILE PATH (the workspace sum file is
// <go.work path> + ".sum"), so a dev build's lock lands in go.work.dev.sum and the committed
// go.work / go.work.sum stay byte-identical.
func devWorkspace(goVer string, devRoots map[string]string) []byte {
	if len(devRoots) == 0 {
		return nil
	}
	names := make([]string, 0, len(devRoots))
	for n := range devRoots {
		names = append(names, n)
	}
	sort.Strings(names)
	var w bytes.Buffer
	fmt.Fprintf(&w, "go %s\n\n", goVer)
	w.WriteString("// Generated by charly/internal/pluginsgen (run by scripts/bootstrap-charly.sh). DO NOT EDIT.\n")
	w.WriteString("//\n")
	w.WriteString("// THE DEV WORKSPACE — it exists ONLY because the build was given a -dev-plugin override.\n")
	w.WriteString("// NOT A RELEASE BUILD: each module below resolves from a LOCAL checkout instead of its\n")
	w.WriteString("// pinned module-proxy tag, so a bed run on this binary exercises UNMERGED plugin source.\n")
	w.WriteString("//\n")
	w.WriteString("// The committed workspace is untouched and so is its checksum lock: `go build` auto-detects\n")
	w.WriteString("// only a file named exactly go.work, so this one is used only under an explicit GOWORK=,\n")
	w.WriteString("// and a workspace's sum file is its own path + \".sum\" (go.work.dev.sum). A plain build\n")
	w.WriteString("// never reads this file, and scripts/bootstrap-charly.sh DELETES it (with its lock) the\n")
	w.WriteString("// first time it runs without an override.\n")
	w.WriteString("//\n")
	w.WriteString("// Overridden:\n")
	for _, n := range names {
		fmt.Fprintf(&w, "//   %s -> %s\n", n, filepath.Join(devRoots[n], "candy", n))
	}
	w.WriteString("use ./charly\n")
	for _, n := range names {
		fmt.Fprintf(&w, "use %s\n", workUsePath(filepath.Join(devRoots[n], "candy", n)))
	}
	return w.Bytes()
}

// workUsePath renders a workspace `use` path as a single token. A path that needs quoting MUST be
// quoted or the workspace file is simply a syntax error, and a dev override is an ARBITRARY local
// path — a checkout under a directory whose name contains a space is an ordinary path, not an
// exotic one. The rule is Go's own (cmd/vendor/golang.org/x/mod/modfile MustQuote, applied by
// AutoQuote — what `go work use` writes); it is spelled out here because pluginsgen deliberately
// imports nothing beyond the stdlib + yaml + spec/refs, and pulling golang.org/x/mod into charly's
// module graph for one predicate would cost more than it explains.
func workUsePath(p string) string {
	for _, r := range p {
		switch r {
		case ' ', '"', '\'', '`':
			return strconv.Quote(p)
		case '(', ')', '[', ']', '{', '}', ',':
			if len(p) > 1 {
				return strconv.Quote(p)
			}
		default:
			if !unicode.IsPrint(r) {
				return strconv.Quote(p)
			}
		}
	}
	if p == "" || strings.Contains(p, "//") || strings.Contains(p, "/*") {
		return strconv.Quote(p)
	}
	return p
}

// readCompiledPlugins reads ONLY the compiled_plugins: list from a charly.yml,
// tolerating every other key (it decodes into a one-field struct).
func readCompiledPlugins(path string) ([]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var doc struct {
		CompiledPlugins []string `yaml:"compiled_plugins"`
	}
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return doc.CompiledPlugins, nil
}

// corpusRepoList returns the plugin repos whose manifests the word->ref index covers: the
// compiled_plugins: list PLUS the org-wide corpus file, so an out-of-tree plugin (a substrate
// served out-of-process, never compiled in) is still discoverable by word. Sorted, so the
// generated index is stable. This is the ONE place that set is built — the generator and the
// index soundness gate (internal/pluginsgen/main_test.go, TestPluginsRefsIndexSound) must not
// disagree about what "the corpus" means.
func corpusRepoList(root, corpusFile string, names []string) []string {
	repoSet := map[string]bool{}
	for _, n := range names {
		repoSet["github.com/opencharly/"+n] = true
	}
	for _, r := range readCorpus(root, corpusFile) {
		if r != "" {
			repoSet[strings.TrimSpace(r)] = true
		}
	}
	repoList := make([]string, 0, len(repoSet))
	for r := range repoSet {
		repoList = append(repoList, r)
	}
	sort.Strings(repoList)
	return repoList
}

// readCorpus returns the plugin repo paths named in the optional corpus file (the
// umbrella's org-wide plugin-* set, one repo path per line). Empty/absent => nil: only
// the compiled_plugins corpus is indexed.
func readCorpus(root, path string) []string {
	if path == "" {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(root, path))
	if err != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out
}

// repoSetCompiled reports whether a repo path (or a candy/provider REF under it) is one of
// the compiled-in plugin repos (github.com/opencharly/<name>) — a compiled repo that cannot
// be indexed is fatal.
// devRepoRoot returns the local checkout a -dev-plugin override points this repo at, whether or not
// this build compiles that plugin in. Keyed by candy name, and a repo is
// github.com/opencharly/<name> (or a subpath of it) — the same predicate repoSetCompiled uses, shared
// rather than re-derived so the two cannot drift (R3).
func devRepoRoot(devRoots map[string]string, repo string) string {
	for name, dir := range devRoots {
		if pluginRepoMatches(repo, name) {
			return dir
		}
	}
	return ""
}

// pluginRepoMatches reports whether repo IS the plugin repo named name — the repository root
// github.com/opencharly/<name>, or a path under it (a candy/provider REF is "<root>/candy/<x>", a
// repo PATH is exactly <root>). ONE predicate: two spellings of it is how a dev override silently
// stops matching the repo it names (opencharly/charly#835).
func pluginRepoMatches(repo, name string) bool {
	root := "github.com/opencharly/" + name
	return repo == root || strings.HasPrefix(repo, root+"/")
}

func repoSetCompiled(names []string, repo string) (string, bool) {
	for _, n := range names {
		// root OR a subpath: a candy/provider REF is "<root>/candy/<x>", while a repo
		// PATH is exactly <root> — one predicate serves both (R3).
		if pluginRepoMatches(repo, n) {
			return n, true
		}
	}
	return "", false
}

// indexRepoPluginRefs returns the word->candy-ref map from a plugin repo CHECKOUT's OWN
// candy/*/charly.yml `plugin:` blocks (providers: + source:). The checkout is the repo fetched at
// its default branch — read without a pinned version, since the corpus is a set of repos and the
// ref recorded is the plugin's own declared `source:` (path-only, tagless), which the runtime
// resolver re-fetches at the charly-go.mod-pinned tag — or, under a -dev-plugin override, the
// LOCAL checkout, so both callers read a tree the same way.
func indexRepoPluginRefs(root string) (map[string]string, error) {
	out := map[string]string{}
	candyRoot := filepath.Join(root, "candy")
	entries, err := os.ReadDir(candyRoot)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		yml := filepath.Join(candyRoot, e.Name(), "charly.yml")
		b, err := os.ReadFile(yml)
		if err != nil {
			continue
		}
		// Tolerant decode: a candy charly.yml's top level is a MIX of entity mappings
		// (the candy, sibling skill/hook entities) AND scalars (`version:`). Decode into
		// yaml.Node and only read the mapping entries that carry candy.plugin.
		var doc map[string]yaml.Node
		if err := yaml.Unmarshal(b, &doc); err != nil {
			return nil, fmt.Errorf("parse %s: %w", yml, err)
		}
		for _, node := range doc {
			if node.Kind != yaml.MappingNode {
				continue
			}
			var ent struct {
				Candy struct {
					Plugin struct {
						Providers []string `yaml:"providers"`
						Source    string   `yaml:"source"`
					} `yaml:"plugin"`
				} `yaml:"candy"`
			}
			if err := node.Decode(&ent); err != nil {
				continue
			}
			src := strings.TrimSpace(ent.Candy.Plugin.Source)
			if src == "" || src == "builtin" {
				continue
			}
			for _, w := range ent.Candy.Plugin.Providers {
				w = strings.TrimSpace(w)
				if w == "" {
					continue
				}
				out[w] = src
			}
		}
	}
	return out, nil
}

// pluginModulePath returns a compiled plugin candy's module path: the candy's
// `plugin.source:` field when it names a module path (the candy de-submodule
// cutover's standalone shape — e.g. github.com/opencharly/plugin-port/candy/plugin-port),
// falling back to candy/<name>/go.mod when source is absent or `builtin` (the
// in-repo state). source: is the module path of record for compiled-in plugins;
// the go.mod fallback keeps `source: builtin` candies (plugin-agentteams,
// plugin-dsh) resolving against their in-repo module.
func pluginModulePath(candyDir, name string) (string, error) {
	src, err := readPluginSource(filepath.Join(candyDir, "charly.yml"), name)
	if err != nil {
		return "", err
	}
	if src != "" {
		return src, nil
	}
	return readModulePath(filepath.Join(candyDir, "go.mod"))
}

// readPluginSource returns the module path from a compiled plugin candy's
// `plugin.source:` field, or "" when the field is absent or `builtin` (the two
// forms meaning "no standalone module path — fall back to candy/<name>/go.mod").
// The top-level charly.yml key is the candy name; sibling entities (e.g. a
// `<name>-cli-skill` block) carry other top-level keys and no candy.plugin.source,
// so the lookup is by the candy name itself.
func readPluginSource(path, name string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read candy charly.yml: %w", err)
	}
	var doc map[string]struct {
		Candy struct {
			Plugin struct {
				Source string `yaml:"source"`
			} `yaml:"plugin"`
		} `yaml:"candy"`
	}
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return "", fmt.Errorf("parse candy charly.yml %s: %w", path, err)
	}
	e, ok := doc[name]
	if !ok {
		return "", nil
	}
	src := strings.TrimSpace(e.Candy.Plugin.Source)
	if src == "builtin" {
		return "", nil
	}
	return src, nil
}

// moduleVersion returns the pinned version for a module path from a go.mod's require list —
// the Go-tag version (v0.2026j.hhmm form) the compiled-in plugin modules are pinned at.
func moduleVersion(gomodPath, module string) (string, error) {
	f, err := os.Open(gomodPath)
	if err != nil {
		return "", fmt.Errorf("open go.mod: %w", err)
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if rest, ok := strings.CutPrefix(line, module+" "); ok {
			ver := strings.TrimSpace(rest)
			if ver == "" {
				return "", fmt.Errorf("empty version for %s in %s", module, gomodPath)
			}
			return ver, nil
		}
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("no require pin for %s in %s (a compiled plugin must be pinned in charly/go.mod)", module, gomodPath)
}

// readModulePath returns the `module` path declared in a go.mod.
func readModulePath(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open go.mod: %w (a compiled plugin must be its own Go module)", err)
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if rest, ok := strings.CutPrefix(line, "module "); ok {
			return strings.TrimSpace(rest), nil
		}
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("no module directive in go.mod")
}

// readGoDirective returns the `go` version directive from a go.mod (for go.work's go line).
func readGoDirective(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if rest, ok := strings.CutPrefix(line, "go "); ok {
			return strings.TrimSpace(rest), nil
		}
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("no go directive in %s", path)
}

// requirePluginBlock asserts a candy's charly.yml declares a plugin: block — guarding
// against listing a non-plugin candy in compiled_plugins (which would compile in a
// package with no NewProvider/NewMeta). A coarse textual check is sufficient here;
// `charly box validate` does the authoritative plugin-candy validation.
func requirePluginBlock(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read candy charly.yml: %w", err)
	}
	if !bytes.Contains(b, []byte("plugin:")) {
		return fmt.Errorf("candy has no plugin: block — only plugin candies may be compiled in")
	}
	return nil
}

// detectShape reports a candy's plugin shape from a textual scan of its Go at codegen
// time (never runtime; avoids importing the candy module, keeping pluginsgen stdlib+yaml
// only): "kit" if it exports `func NewCheckVerb(` (a host-coupled check verb WITH a
// schema, compiled-in-only), else "pb" (a dual-placement NewProvider/NewMeta plugin).
func detectShape(candyDir string) (string, error) {
	shape := "pb"
	err := filepath.WalkDir(candyDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		if bytes.Contains(b, []byte("func NewCheckVerb(")) {
			shape = "kit"
		}
		return nil
	})
	return shape, err
}

// goAlias turns a candy dir name into a stable, valid Go import alias (cp_<sanitized>).
func goAlias(name string) string {
	var b strings.Builder
	b.WriteString("cp_")
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}
