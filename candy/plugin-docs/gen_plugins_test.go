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

	// Independently re-derive the census from the same slice.
	wantWords := 0
	wantCompiled := 0
	for _, p := range plugins {
		wantWords += len(p.Providers())
		if p.CompiledIn {
			wantCompiled++
		}
	}
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

	// Per-class headers carry the per-class word count, so the class breakdown is computed too.
	for _, want := range []string{
		"## `verb` — 3 words",
		"## `kind` — 1 words",
		"## `command` — 1 words",
		"## `step` — 1 words",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("per-class header missing %q in:\n%s", want, got)
		}
	}
}
