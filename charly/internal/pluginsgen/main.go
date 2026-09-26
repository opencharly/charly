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
	"strings"

	"github.com/opencharly/spec/refs"
	"gopkg.in/yaml.v3"
)

func main() {
	cfg := flag.String("config", "charly/charly.yml", "charly.yml whose compiled_plugins: drives generation (relative to -root)")
	root := flag.String("root", ".", "repo root (contains charly/ and candy/); go.work use-paths are relative to it")
	outGo := flag.String("out", "charly/plugins_generated.go", "generated registration file (relative to -root)")
	outWork := flag.String("gowork", "go.work", "generated go.work (relative to -root)")
	outRefs := flag.String("outrefs", "charly/plugins_refs_generated.go", "generated word->candy-ref index (relative to -root)")
	corpus := flag.String("corpus", "", "optional newline list of plugin repo paths (relative to -root) whose manifests are indexed; the org-wide plugin-* set. Empty => only the compiled_plugins corpus is indexed.")
	flag.Parse()

	if err := run(*root, *cfg, *outGo, *outWork, *outRefs, *corpus); err != nil {
		fmt.Fprintf(os.Stderr, "pluginsgen: %v\n", err)
		os.Exit(1)
	}
}

func run(root, cfg, outGo, outWork, outRefs, corpus string) error {
	genGo, genWork, genRefs, err := generate(root, cfg, corpus)
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
	return nil
}

// generate produces the byte content of plugins_generated.go + go.work from a
// charly.yml's compiled_plugins: list, WITHOUT writing — so the reproducibility gate
// (TestPluginsGenReproducible) can diff against the committed files.
func generate(root, cfg, corpusFile string) (genGo, genWork, genRefs []byte, err error) {
	names, err := readCompiledPlugins(filepath.Join(root, cfg))
	if err != nil {
		return nil, nil, nil, err
	}

	// pluginRepos is the set of plugin repos whose manifests are indexed: the
	// compiled_plugins: corpus PLUS any org-wide corpus (the umbrella's plugin-* set),
	// so an out-of-tree plugin (a substrate served out-of-process, never compiled in)
	// is still discoverable by word. Every repo is read for its OWN `plugin:` blocks —
	// the word->ref FACT lives only in each plugin's manifest, never in charly.
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

	// wordRefs is the generated provider-ref INDEX: "<class>:<word>" -> canonical candy
	// ref, derived from every plugin repo's own manifest. It replaces charly's former
	// hand-written per-kind maps (externalDeploySubstratePlugins / vmPluginCandyRef) —
	// a pure PROJECTION of the plugin repos, not an authored copy (boundary-law clause D).
	wordRefs := map[string]string{}
	for _, repo := range repoList {
		refsForRepo, err := indexRepoPluginRefs(repo)
		if err != nil {
			// A repo that cannot be fetched/parsed is a genuine error only for a
			// compiled-in plugin (needed to build); an out-of-tree corpus repo that is
			// unavailable simply contributes no words.
			if _, compiled := repoSetCompiled(names, repo); compiled {
				return nil, nil, nil, fmt.Errorf("index plugin %s: %w", repo, err)
			}
			continue
		}
		for word, ref := range refsForRepo {
			if prev, dup := wordRefs[word]; dup && prev != ref {
				return nil, nil, nil, fmt.Errorf("provider word %s is served by two plugin refs (%s and %s) — a word has one canonical provider", word, prev, ref)
			}
			wordRefs[word] = ref
		}
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
		// sdk/spec contract-module shape). The repo is fetched at that version via the SAME
		// standalone fetch the runtime uses (spec/refs.DownloadRepo), and the plugin-block check
		// + kit/pb shape detection read the FETCHED repo's candy/<name>/ tree.
		mod := "github.com/opencharly/" + name + "/candy/" + name
		version, err := moduleVersion(filepath.Join(root, "charly", "go.mod"), mod)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("compiled plugin %q: %w", name, err)
		}
		// The Go tag is subdir-prefixed: candy/<name>/<version> (the module lives at candy/<name>
		// inside the repo — the convention the plugin-generate-packages precedent set).
		repoPath := "github.com/opencharly/" + name
		fetched, err := refs.DownloadRepo(repoPath, "candy/"+name+"/"+version)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("compiled plugin %q: fetch %s@%s: %w", name, repoPath, version, err)
		}
		candyDir := filepath.Join(fetched, "candy", name)
		if err := requirePluginBlock(filepath.Join(candyDir, "charly.yml")); err != nil {
			return nil, nil, nil, fmt.Errorf("compiled plugin %q: %w", name, err)
		}
		shape, err := detectShape(candyDir)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("compiled plugin %q: %w", name, err)
		}
		entries = append(entries, entry{name: name, module: mod, alias: goAlias(name), shape: shape})
	}

	// --- plugins_generated.go ---
	var g bytes.Buffer
	g.WriteString("// Code generated by pluginsgen (charly box generate-plugins). DO NOT EDIT.\n//\n")
	g.WriteString("// One registerCompiledPlugin() call per plugin candy named in charly.yml\n")
	g.WriteString("// `compiled_plugins:`. Regenerated by `scripts/bootstrap-charly.sh` before `go build`.\n")
	g.WriteString("// Each compiled-in plugin module is a `require` pin in charly/go.mod resolved\n")
	g.WriteString("// from the module proxy (no `use` directive in go.work — see the generator's\n")
	g.WriteString("// go.work writer). Edit charly.yml's\n")
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
	formatted, err := format.Source(g.Bytes())
	if err != nil {
		return nil, nil, nil, fmt.Errorf("format generated go: %w\n%s", err, g.String())
	}

	// --- plugins_refs_generated.go — the word->candy-ref INDEX. A pure projection of the
	// plugin repos' own `plugin:` blocks (boundary-law clause D): the word->ref FACT lives
	// in each plugin repo, never in charly. Replaces the former hand-written per-kind maps
	// (externalDeploySubstratePlugins / vmPluginCandyRef). Regenerated with the rest of the
	// wiring; a new plugin needs its repo in the corpus, no charly code change.
	var r bytes.Buffer
	r.WriteString("// Code generated by pluginsgen (charly box generate-plugins). DO NOT EDIT.\n//\n")
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
		return nil, nil, nil, fmt.Errorf("format generated refs: %w\n%s", err, r.String())
	}

	// --- go.work ---
	goVer, err := readGoDirective(filepath.Join(root, "charly", "go.mod"))
	if err != nil {
		return nil, nil, nil, err
	}
	var w bytes.Buffer
	fmt.Fprintf(&w, "go %s\n\n", goVer)
	w.WriteString("// Generated by pluginsgen (charly box generate-plugins). DO NOT EDIT.\n")
	w.WriteString("// The charly module plus the compiled-in plugin candies, resolved as PROXY\n")
	w.WriteString("// modules (the candy de-submodule cutover, Phase 4): every compiled-in plugin\n")
	w.WriteString("// module (github.com/opencharly/<name>/candy/<name>) is a `require` pin in\n")
	w.WriteString("// charly/go.mod at its Go tag, resolved from the module proxy — mirroring the\n")
	w.WriteString("// sdk/spec contract modules. There are no workspace members beyond charly; the\n")
	w.WriteString("// `use ./candy/...` in-repo shape was deleted with the in-repo candy dirs.\n")
	w.WriteString("use ./charly\n")
	return formatted, w.Bytes(), formattedRefs, nil
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

// repoSetCompiled reports whether a repo path is one of the compiled-in plugin repos
// (github.com/opencharly/<name>) — a compiled repo that cannot be indexed is fatal.
func repoSetCompiled(names []string, repo string) (string, bool) {
	for _, n := range names {
		if repo == "github.com/opencharly/"+n {
			return n, true
		}
	}
	return "", false
}

// indexRepoPluginRefs fetches a plugin repo (at its default branch) and returns the
// word->candy-ref map from its OWN candy/*/charly.yml `plugin:` blocks (providers: +
// source:). It reads the repo without a pinned version — the corpus is a set of repos,
// and the ref recorded is the plugin's own declared `source:` (path-only, tagless), which
// the runtime resolver re-fetches at the charly-go.mod-pinned tag.
func indexRepoPluginRefs(repo string) (map[string]string, error) {
	fetched, err := refs.DownloadRepo(repo, "HEAD")
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	candyRoot := filepath.Join(fetched, "candy")
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
