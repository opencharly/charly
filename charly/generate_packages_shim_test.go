package main

// generate_packages_shim_test.go — tools/generate-packages is the superproject's THIN
// RE-EXPORT SHIM for the out-of-tree `charly generate-packages` command plugin
// (opencharly/plugin-generate-packages). It is what makes the verb resolvable from a
// charly CHECKOUT: resolveCommandPluginBinary consults the baked plugin map FIRST and
// returns immediately on a hit, so on a machine with the charly package installed the
// shim is never consulted — the project fall-through (LoadConfig → ScanAllCandy →
// findCommandPluginCandy → resolvePluginBinary over the candy's source dir) is the ONLY
// path the shim serves, and the only one these assertions cover.
//
// Each assertion below is keyed to a way the shim can rot silently: the declared word
// drifting from the CLI verb, the re-export becoming a stub that no longer imports the
// external module, or the module require being dropped. None of the three is visible to
// `charly box validate`, and none is observable on a host whose baked plugin short-circuits
// the lookup.

import (
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const (
	shimCandyYML  = "../tools/generate-packages/charly.yml"
	shimServeMain = "../tools/generate-packages/cmd/serve/main.go"
	shimGoMod     = "../tools/generate-packages/go.mod"

	// shimWord is the CLI verb. The distro-repo workflows drop a prebuilt plugin whose
	// committed .providers manifest carries exactly this string, so the shim's declared
	// provider and the baked manifest MUST agree or the two resolution paths diverge.
	shimWord = "command:generate-packages"

	// shimUpstreamModule is the external module the shim re-exports. `source:` in the
	// candy is identity metadata that is never fetched, so this go.mod require is the
	// only thing that actually binds the shim to the upstream plugin.
	shimUpstreamModule = "github.com/opencharly/plugin-generate-packages/candy/generate-packages"
)

// TestGeneratePackagesShim_DeclaresTheCommandWord proves the shim declares the command the
// CLI dispatches. findCommandPluginCandy matches on this exact provider string; a drift here
// makes `charly generate-packages` fail to resolve from a checkout with no other symptom.
func TestGeneratePackagesShim_DeclaresTheCommandWord(t *testing.T) {
	data, err := os.ReadFile(shimCandyYML)
	if err != nil {
		t.Fatalf("read %s: %v", shimCandyYML, err)
	}
	var doc map[string]struct {
		Candy struct {
			Plugin struct {
				Providers []string `yaml:"providers"`
			} `yaml:"plugin"`
		} `yaml:"candy"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse %s: %v", shimCandyYML, err)
	}
	entry, ok := doc["generate-packages"]
	if !ok {
		t.Fatalf("%s: no top-level `generate-packages:` entity (the candy name is the CLI-facing identity)", shimCandyYML)
	}
	got := entry.Candy.Plugin.Providers
	if len(got) != 1 || got[0] != shimWord {
		t.Fatalf("%s: plugin.providers = %v, want exactly [%q]", shimCandyYML, got, shimWord)
	}
}

// TestGeneratePackagesShim_ReExportsUpstream proves the shim is a real re-export and not a
// stub: cmd/serve must IMPORT the upstream module. A shim that compiles but no longer imports
// it would build a binary that serves nothing, and `--help` would still print a usage block.
//
// This reads the parsed IMPORT SPECS, not the file text. A substring scan passes on the module
// path appearing in this file's own doc comment — measured: deleting the import left a
// text-scanning version of this assertion green, which is the precise failure it exists to
// catch.
func TestGeneratePackagesShim_ReExportsUpstream(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), shimServeMain, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse %s: %v", shimServeMain, err)
	}
	for _, spec := range f.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		if path == shimUpstreamModule {
			return
		}
	}
	t.Fatalf("%s: no import of %q — the shim must RE-EXPORT the upstream provider, not reimplement it", shimServeMain, shimUpstreamModule)
}

// TestGeneratePackagesShim_RequiresUpstreamModule proves the binding the `source:` field does
// NOT create. `source:` is identity metadata the host never fetches; the go.mod require is
// what the host build actually resolves, so dropping it breaks the verb while leaving the
// candy declaration looking correct.
func TestGeneratePackagesShim_RequiresUpstreamModule(t *testing.T) {
	mod, err := os.ReadFile(shimGoMod)
	if err != nil {
		t.Fatalf("read %s: %v", shimGoMod, err)
	}
	if !strings.Contains(string(mod), shimUpstreamModule) {
		t.Fatalf("%s: no require for %q — `source:` is identity metadata and does not bind the shim to the upstream module", shimGoMod, shimUpstreamModule)
	}
}
