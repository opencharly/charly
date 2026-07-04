package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCandyGoModsAreCanonical asserts every candy/plugin-*/go.mod matches the ONE
// canonical Shape-A module template, so the 74-module plugin surface can never drift:
//
//   - module github.com/opencharly/charly/candy/<dir>
//   - require github.com/opencharly/sdk v0.0.0        (the SDK is the ONLY charly-org dep)
//   - replace github.com/opencharly/sdk => ../../sdk  (resolves the in-tree submodule)
//   - NO dependency on the charly CORE module github.com/opencharly/charly/charly
//     (the Cutover-1 invariant: a plugin imports ONLY the SDK, never core)
//
// The plugin's OWN direct/indirect deps are free to vary (that is the point of the
// per-module dependency shed). plugin-spice's extra `=> ./third_party/spice` replace
// is the SOLE sanctioned outlier (vendored upstream). Drift here is a maintainability
// regression; `task plugins:tidy` is the companion sweep that keeps go.sum in step.
func TestCandyGoModsAreCanonical(t *testing.T) {
	// go test runs in the charly/ package dir; candy/ is its sibling under the repo root.
	mods, err := filepath.Glob(filepath.Join("..", "candy", "plugin-*", "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if len(mods) == 0 {
		t.Fatal("no candy/plugin-*/go.mod found — the glob or layout changed")
	}
	const coreModule = "github.com/opencharly/charly/charly"
	for _, m := range mods {
		dir := filepath.Base(filepath.Dir(m))
		b, err := os.ReadFile(m)
		if err != nil {
			t.Fatalf("%s: %v", m, err)
		}
		src := string(b)
		if want := "module github.com/opencharly/charly/candy/" + dir; !strings.Contains(src, want) {
			t.Errorf("%s: missing canonical module line %q", m, want)
		}
		if !strings.Contains(src, "github.com/opencharly/sdk v0.0.0") {
			t.Errorf("%s: missing `require github.com/opencharly/sdk v0.0.0`", m)
		}
		if !strings.Contains(src, "replace github.com/opencharly/sdk => ../../sdk") {
			t.Errorf("%s: missing `replace github.com/opencharly/sdk => ../../sdk`", m)
		}
		if strings.Contains(src, coreModule) {
			t.Errorf("%s: depends on the charly CORE module %q — a plugin imports ONLY the sdk", m, coreModule)
		}
	}
}
