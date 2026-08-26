package main

// The KONG FLAG-TAG gate — the mechanical guard against a silently-ignored flag
// rename reappearing.
//
// Kong renames a flag with `name:"..."`. The go-flags/jessevdk spelling is
// `long:"..."`, and Kong does not read it: a struct tag carrying `long:` is
// inert, so Kong falls back to deriving the flag from the FIELD NAME. Nothing
// errors. The author writes an intent the binary discards.
//
// That cost real, published commands. `charly box new box --candy` and
// `charly box write --from-stdin` were both documented on opencharly.ai and
// both exited 80, because their fields (Candies, FromIn) derived --candies and
// --from-in. `charly config --volume` and `charly secrets gpg setup
// --skip-secret-service` were documented and broken the same way.
//
// The whole tree was converted to `name:` in one cutover. This gate keeps it
// converted: any `long:` key in any struct tag under charly/ or candy/ fails,
// with the file:line and the flag Kong would ACTUALLY have shipped, so the
// author sees the divergence rather than discovering it from a bug report.
//
// Scope is this repo's own Go trees (charly/ + candy/). The submodules carry no
// Kong grammar; a tag added there is that repo's gate to add.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/alecthomas/kong"
)

// kongGrammarTrees are the directories, relative to the repo root, that hold
// this repo's Kong grammar structs. The candy de-submodule cutover (Phase 4) deleted the
// in-repo candy/ dirs — the compiled-in plugin grammars now resolve from the STANDALONE
// plugin modules (proxy-pinned in charly/go.mod), so this gate walks the charly tree AND
// every resolved plugin module dir in the go module cache.
var kongGrammarTrees = []string{"charly"}

// scanFloor guards against a VACUOUS pass. If the walk finds fewer than this
// many `name:`-tagged fields, it did not read the tree it reports on — a moved
// directory or a broken path would otherwise show up as a green test.
const scanFloor = 250

func TestKongFlagTags_NoIgnoredLongTagSurvives(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "charly")); err != nil {
		t.Fatalf("repo root %s has no charly/ — the gate is not reading the tree it reports on: %v", root, err)
	}

	var offenders []string
	nameTags, filesScanned := 0, 0

	// Phase 4: the compiled-in plugin grammars live in the standalone plugin modules, resolved
	// through the Go module cache (proxy-pinned in charly/go.mod). Walk every cache dir whose
	// module path is github.com/opencharly/plugin-<name>/candy/plugin-<name>.
	pluginTrees := pluginModuleGrammarTrees()

	for _, tree := range append(kongGrammarTrees, pluginTrees...) {
		// The plugin module trees are absolute (the go module cache); the kongGrammarTrees are
		// relative to the repo root.
		base := tree
		if !filepath.IsAbs(base) {
			base = filepath.Join(root, base)
		}
		err := filepath.WalkDir(base, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				if n := d.Name(); n == "vendor" || n == "node_modules" || strings.HasPrefix(n, ".") {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") {
				return nil
			}
			filesScanned++

			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return nil // not this gate's job to report unparseable Go; the build catches it
			}
			ast.Inspect(f, func(n ast.Node) bool {
				st, ok := n.(*ast.StructType)
				if !ok || st.Fields == nil {
					return true
				}
				for _, field := range st.Fields.List {
					if field.Tag == nil {
						continue
					}
					raw, err := strconv.Unquote(field.Tag.Value)
					if err != nil {
						continue
					}
					tag := reflect.StructTag(raw)
					if _, ok := tag.Lookup("name"); ok {
						nameTags++
					}
					long, ok := tag.Lookup("long")
					if !ok {
						continue
					}
					fieldName := "<embedded>"
					if len(field.Names) > 0 {
						fieldName = field.Names[0].Name
					}
					rel, relErr := filepath.Rel(root, path)
					if relErr != nil {
						rel = path
					}
					offenders = append(offenders, strings.Join([]string{
						rel + ":" + strconv.Itoa(fset.Position(field.Tag.Pos()).Line),
						"field " + fieldName,
						`long:"` + long + `" is inert — Kong ships --` + kongFlagName(fieldName),
					}, "  "))
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", base, err)
		}
	}

	if nameTags < scanFloor {
		t.Fatalf("found only %d name:-tagged fields across %d files in %v — below the %d floor, so this gate did not read the Kong grammar and its pass would be vacuous",
			nameTags, filesScanned, kongGrammarTrees, scanFloor)
	}

	if len(offenders) > 0 {
		t.Errorf("%d struct tag(s) use go-flags `long:` syntax, which Kong ignores — Kong derives the flag from the FIELD NAME instead, so the declared intent never ships. Use `name:` (and pick the spelling deliberately: changing it renames a shipped flag).\n  %s",
			len(offenders), strings.Join(offenders, "\n  "))
	}
}

// kongFlagName asks KONG what flag a field name yields, so the failure message
// names the flag that would ACTUALLY ship rather than one this file predicts.
// Kong's derivation is deliberately not reimplemented here: a hand-rolled model
// of it is how the original defect was first mis-measured (a naive camelCase
// regex inflated the divergence count from 15 to 35, because Kong keeps
// consecutive capitals together — RunID is --run-id, not --r-u-n-i-d).
//
// A one-field struct is built at runtime and handed to kong.New; the derived
// flag is read back off the model. Runs only when a violation is being
// reported, so its cost never touches the passing path.
func kongFlagName(field string) string {
	if field == "" || !ast.IsExported(field) {
		return "?"
	}
	t := reflect.StructOf([]reflect.StructField{{
		Name: field,
		Type: reflect.TypeOf(""),
		Tag:  reflect.StructTag(`help:"x"`),
	}})
	k, err := kong.New(reflect.New(t).Interface(), kong.Name("probe"), kong.Exit(func(int) {}))
	if err != nil {
		return "?"
	}
	for _, f := range k.Model.Flags {
		if f.Name != "help" {
			return f.Name
		}
	}
	return "?"
}

// pluginModuleGrammarTrees returns the on-disk dirs of the compiled-in plugin modules resolved
// through the Go module cache (github.com/opencharly/plugin-<name>/candy/plugin-<name>@<version>).
// The candy de-submodule cutover (Phase 4) moved the plugin grammars out of the in-repo candy/
// dirs; this gate now walks them from the proxy cache, so an inert `long:` tag in a compiled-in
// plugin's Kong grammar still fails the gate.
func pluginModuleGrammarTrees() []string {
	var out []string
	gopath, err := exec.Command("go", "env", "GOMODCACHE").Output()
	if err != nil {
		return nil
	}
	base := filepath.Join(strings.TrimSpace(string(gopath)), "github.com", "opencharly")
	entries, err := os.ReadDir(base)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "plugin-") {
			continue
		}
		// The module cache layout: <repo>/candy/plugin-<name>@<version>/ — the repo dir holds
		// the candy subdir, which holds the @version dirs. Walk the newest @version dir.
		// The module cache layout for the module path
		// github.com/opencharly/plugin-<name>/candy/plugin-<name> is
		// <modcache>/github.com/opencharly/<repo>/candy/plugin-<name>@<version> — the @version
		// suffix lands on the LAST path element. Walk <repo>/candy/ for plugin-<name>@<version>
		// dirs.
		candyDir := filepath.Join(base, e.Name(), "candy")
		ents, err := os.ReadDir(candyDir)
		if err != nil {
			continue
		}
		prefix := e.Name() + "@"
		for _, v := range ents {
			if !v.IsDir() || !strings.HasPrefix(v.Name(), prefix) || strings.HasSuffix(v.Name(), ".info") || strings.HasSuffix(v.Name(), ".mod") || strings.HasSuffix(v.Name(), ".zip") {
				continue
			}
			out = append(out, filepath.Join(candyDir, v.Name()))
		}
	}
	return out
}
