package docs

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/opencharly/spec/spec"
)

// TestGenerateProviderIndexCensusComputed locks in that the census line in the provider index is
// COMPUTED from the plugins slice, never transcribed. A hand-written count would survive a catalog
// change silently; this test re-derives the three numbers from the same slice the page is built
// from and fails the moment they diverge — the same drift the census exists to kill.
func TestGenerateProviderIndexCensusComputed(t *testing.T) {
	plugins := []pluginEntity{
		{entity: entity{Name: "plugin-a", Candy: &candyView{Plugin: &spec.Plugin{Providers: []spec.PluginCapability{"verb:file", "verb:http"}}}}, CompiledIn: true},
		{entity: entity{Name: "plugin-b", Candy: &candyView{Plugin: &spec.Plugin{Providers: []spec.PluginCapability{"kind:candy", "command:check"}}}}, CompiledIn: true},
		{entity: entity{Name: "plugin-c", Candy: &candyView{Plugin: &spec.Plugin{Providers: []spec.PluginCapability{"verb:file", "step:file"}}}}, CompiledIn: false},
	}
	out := t.TempDir()

	words, err := generateProviderIndex(out, plugins)
	if err != nil {
		t.Fatalf("generateProviderIndex: %v", err)
	}

	// Independently re-derive the census from the same slice. DISTINCT words, not rows:
	// this fixture deliberately carries a duplicate — `verb:file` is served by BOTH plugin-a
	// and plugin-c — and a word served twice is still one word. Counting rows is what made
	// the index headline say "56 words" over a 55-word class in production, and made the
	// console disagree with the page it had just written.
	distinct := map[string]bool{}
	wantCompiled := 0
	for _, p := range plugins {
		for _, prov := range p.Providers() {
			distinct[string(prov)] = true
		}
		if p.CompiledIn {
			wantCompiled++
		}
	}
	wantWords := len(distinct)
	if words != wantWords {
		t.Errorf("returned word count = %d, want %d", words, wantWords)
	}

	raw, err := os.ReadFile(filepath.Join(out, "reference", "providers.md"))
	if err != nil {
		t.Fatalf("read emitted providers.md: %v", err)
	}
	got := string(raw)

	census := "**" + strconv.Itoa(wantWords) + " words across " + strconv.Itoa(len(plugins)) +
		" plugin candies, " + strconv.Itoa(wantCompiled) + " compiled into the binary.**"
	if !strings.Contains(got, census) {
		t.Errorf("census line missing %q in:\n%s", census, got)
	}

	// Per-class headers carry the per-class DISTINCT word count, so the class breakdown is
	// computed too. Singular classes read "1 word", not "1 words".
	//
	// `verb` is 2, not 3: the fixture serves `verb:file` from BOTH plugin-a and plugin-c, so
	// the class has three ROWS and two WORDS. That gap is the whole point of this fixture —
	// asserting 3 here is what let the production index publish "56 words" above 55 pages.
	for _, want := range []string{
		"## `verb` — 2 words",
		"## `kind` — 1 word",
		"## `command` — 1 word",
		"## `step` — 1 word",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("per-class header missing %q in:\n%s", want, got)
		}
	}
}
