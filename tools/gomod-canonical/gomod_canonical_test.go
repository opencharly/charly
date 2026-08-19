package gomodcanonical

import (
	"os"
	"path/filepath"
	"regexp"
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
// regression; `task mods:tidy` is the companion sweep that keeps go.sum in step.
//
// Relocated (#55 decoupling cone, Batch D) from charly/plugin_gomod_canonical_test.go:
// this is pure plugin-go.mod-file HYGIENE (string assertions on go.mod TEXT — it makes
// no Go import of any sdk package, and imports nothing itself), unrelated to any charly
// core logic, so it lives in its own standalone module rather than in charly/ or the
// plugins/ submodule (which carries no Go code at all — skills/docs/config only). The
// glob is adjusted for the new location: tools/gomod-canonical/ is two levels below the
// repo root (tools/gomod-canonical -> tools -> root), where charly/ was only one level
// below (charly -> root).
func TestCandyGoModsAreCanonical(t *testing.T) {
	mods, err := filepath.Glob(filepath.Join("..", "..", "candy", "plugin-*", "go.mod"))
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

// specRequireRe extracts the `github.com/opencharly/spec <version>` require line from a go.mod.
// The trailing `// indirect` marker is deliberately not part of the capture: whether a module
// imports spec directly or transitively does not change which VERSION it must agree on.
var specRequireRe = regexp.MustCompile(`(?m)^\s*github\.com/opencharly/spec\s+(v\S+)`)

// localContractReplaceRe matches a `replace` of either contract module onto a local path. It is
// the membership test for the lockstep set: a module that resolves sdk or spec from the checkout
// is one whose spec NUMBER Go still validates, and is therefore one this gate covers.
var localContractReplaceRe = regexp.MustCompile(`(?m)^replace github\.com/opencharly/(sdk|spec) =>`)

// lockstepModules returns every in-repo module that resolves the contract modules through a local
// replace — the exact set `task mods:tidy` sweeps. The glob is deliberately NOT `candy/plugin-*`:
// that shape misses `candy/generate-packages` (a candy module that is not a plugin) and the
// `tools/golden-*` fixture modules, and all three were found broken by exactly that blind spot.
func lockstepModules(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, pat := range []string{
		filepath.Join("..", "..", "candy", "*", "go.mod"),
		filepath.Join("..", "..", "tools", "*", "go.mod"),
	} {
		found, err := filepath.Glob(pat)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range found {
			b, err := os.ReadFile(m)
			if err != nil {
				t.Fatalf("%s: %v", m, err)
			}
			if localContractReplaceRe.MatchString(string(b)) {
				out = append(out, m)
			}
		}
	}
	if len(out) == 0 {
		t.Fatal("no module with a local sdk/spec replace found — the glob or layout changed")
	}
	return out
}

// TestSpecPinsMatchSDK asserts every module in the lockstep set requires the SAME
// github.com/opencharly/spec version that sdk/go.mod does.
//
// This is not style. Such a module requires `github.com/opencharly/sdk` with a local `replace`,
// so sdk's OWN spec requirement enters its module graph. Under MVS the module must then list a
// spec version at least as new as sdk's — and it is the local `replace` to ../../spec that makes
// a stale number survive review, because the CODE still resolves from the checkout while the
// NUMBER is what Go validates.
//
// The failure this catches is real and was observed twice in one afternoon. Bumping sdk/go.mod's
// spec require without sweeping left 92 plugin modules behind, and `GOWORK=off go build` died in
// each on "go: updates to go.mod needed". Sweeping only `candy/plugin-*` then left three more —
// `candy/generate-packages` and the two `tools/golden-*` modules — of which the golden pair had
// ALREADY been stale on main for weeks, at spec v0.2026217.301 and v0.2026218.138 against sdk's
// v0.2026229.1754. Nothing noticed, because go.work puts every workspace member in one graph
// where these pins are never consulted (opencharly/charly#324), and the build-time plugin loader
// demotes each compile failure to a warning before failing downstream on an unrelated-looking
// "no provider registered for builder:pixi" (opencharly/charly#326).
//
// The remedy on failure is `task mods:tidy`, which recomputes every member's requires from the
// sdk it replaces.
func TestSpecPinsMatchSDK(t *testing.T) {
	sdkMod := filepath.Join("..", "..", "sdk", "go.mod")
	b, err := os.ReadFile(sdkMod)
	if err != nil {
		t.Fatalf("read %s: %v", sdkMod, err)
	}
	m := specRequireRe.FindStringSubmatch(string(b))
	if m == nil {
		t.Fatalf("%s: no `github.com/opencharly/spec <version>` require found — the sdk's spec "+
			"dependency is the version every lockstep module must agree on", sdkMod)
	}
	want := m[1]

	for _, mod := range lockstepModules(t) {
		b, err := os.ReadFile(mod)
		if err != nil {
			t.Fatalf("%s: %v", mod, err)
		}
		got := specRequireRe.FindStringSubmatch(string(b))
		if got == nil {
			// A module that reaches spec only through the sdk lists no require of its own; that
			// is consistent by construction, so there is nothing to compare.
			continue
		}
		if got[1] != want {
			t.Errorf("%s: requires spec %s, but sdk/go.mod requires %s — run `task mods:tidy`",
				mod, got[1], want)
		}
	}
}
