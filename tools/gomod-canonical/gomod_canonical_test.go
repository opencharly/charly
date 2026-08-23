package gomodcanonical

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestCandyGoModsAreCanonical asserts every candy/plugin-*/go.mod matches the ONE
// canonical Shape-A' module template, so the ~74-module plugin surface can never drift:
//
//   - module github.com/opencharly/charly/candy/<dir>
//   - require github.com/opencharly/sdk v0.2026234.347 (the ONE shared sdk pin — the
//     require version IS the resolution; the sdk contract module comes from the
//     module proxy, there is no local checkout and NO replace)
//   - NO dependency on the charly CORE module github.com/opencharly/charly/charly
//     (the Cutover-1 invariant: a plugin imports ONLY the SDK, never core)
//
// The plugin's OWN direct/indirect deps are free to vary (that is the point of the
// per-module dependency shed). plugin-spice's extra `=> ./third_party/spice` replace
// is the SOLE sanctioned outlier (vendored upstream) — it replaces a third-party
// module, never a contract module. Drift here is a maintainability regression;
// `task mods:tidy` is the companion sweep that keeps go.sum in step.
//
// Relocated (#55 decoupling cone, Batch D) from charly/plugin_gomod_canonical_test.go:
// this is pure plugin-go.mod-file HYGIENE (string assertions on go.mod TEXT — it makes
// no Go import of any sdk package, and imports nothing itself), so it lives in its own
// standalone module rather than in charly/ or the marketplace repo (which carries no
// Go code at all — skills/docs/config only). The glob is adjusted for the new
// location: tools/gomod-canonical/ is two levels below the repo root
// (tools/gomod-canonical -> tools -> root), where charly/ was only one level below
// (charly -> root).
func TestCandyGoModsAreCanonical(t *testing.T) {
	mods, err := filepath.Glob(filepath.Join("..", "..", "candy", "plugin-*", "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if len(mods) == 0 {
		t.Fatal("no candy/plugin-*/go.mod found — the glob or layout changed")
	}
	const coreModule = "github.com/opencharly/charly/charly"
	const sdkPin = "github.com/opencharly/sdk v0.2026234.347"
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
		if !strings.Contains(src, sdkPin) {
			t.Errorf("%s: missing `require %s` (the shared sdk pin)", m, sdkPin)
		}
		if strings.Contains(src, coreModule) {
			t.Errorf("%s: depends on the charly CORE module %q — a plugin imports ONLY the sdk", m, coreModule)
		}
	}
}

// contractRequireRe extracts a `github.com/opencharly/<mod> <version>` require line.
// The trailing `// indirect` marker is deliberately not part of the capture: whether a
// module imports a contract directly or transitively does not change which VERSION it
// must agree on.
var contractRequireRe = regexp.MustCompile(`(?m)^\s*github\.com/opencharly/(sdk|spec)\s+(v\S+)`)

// contractReplaceRe matches a `replace` of either contract module onto a local path.
// After the sdk de-submodule cutover NO in-repo module may replace either contract:
// both resolve from the module proxy at the pinned require versions.
var contractReplaceRe = regexp.MustCompile(`(?m)^replace github\.com/opencharly/(sdk|spec) =>`)

// lockstepModules returns every in-repo module that requires either contract module —
// the exact set `task mods:tidy` sweeps. The glob is deliberately NOT `candy/plugin-*`:
// that shape misses `candy/generate-packages` (a candy module that is not a plugin) and
// the `tools/golden-*` fixture modules, and all three were found broken by exactly that
// blind spot.
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
			if contractRequireRe.MatchString(string(b)) {
				out = append(out, m)
			}
		}
	}
	if len(out) == 0 {
		t.Fatal("no module requiring a contract module found — the glob or layout changed")
	}
	return out
}

// TestContractPinsMatchEverywhere asserts every module that requires a contract module
// lists the SAME shared sdk pin and the SAME shared spec pin, and that NO in-repo
// module replaces either contract onto a local path.
//
// This is not style. A module requiring `github.com/opencharly/sdk` at the shared pin
// inherits sdk's OWN spec requirement into its module graph. Under MVS the module must
// then list a spec version at least as new as sdk's — and with the contracts resolved
// from the module proxy (no local checkout whose code could hide a stale number) the
// require version IS the resolution, so a stale pin fails `GOWORK=off go build` with
// "go: updates to go.mod needed", which the build-time plugin loader demotes to a
// warning before failing downstream on an unrelated-looking "no provider registered"
// (opencharly/charly#326).
//
// The failure this catches is real and was observed twice in one afternoon before the
// shared-pin gate existed. The remedy on failure is `task mods:tidy`.
func TestContractPinsMatchShared(t *testing.T) {
	// The spec anchor is charly/go.mod — the core module's require, the single in-tree
	// source of truth for the shared spec pin.
	coreMod := filepath.Join("..", "..", "charly", "go.mod")
	cb, err := os.ReadFile(coreMod)
	if err != nil {
		t.Fatalf("read %s: %v", coreMod, err)
	}
	coreSpec := contractRequireRe.FindStringSubmatch(string(cb))
	if coreSpec == nil || coreSpec[1] != "spec" {
		t.Fatalf("%s: no `github.com/opencharly/spec <version>` require found — the core module "+
			"is the shared-spec-pin anchor", coreMod)
	}
	wantSpec := coreSpec[2]

	// The sdk pin has no in-tree anchor (core imports zero sdk packages) — it is the
	// adopted shared-pin constant: the sdk tag this tree consumes (sdk's own Go-module
	// tag scheme v0.<YYYYDDD>.<HHMM>). Adopting a newer sdk release bumps this constant
	// in the same sweep as the per-module requires (`task mods:tidy`).
	const wantSDK = "v0.2026234.347"

	for _, mod := range lockstepModules(t) {
		b, err := os.ReadFile(mod)
		if err != nil {
			t.Fatalf("%s: %v", mod, err)
		}
		src := string(b)
		if contractReplaceRe.MatchString(src) {
			t.Errorf("%s: local `replace` of a contract module — both contracts resolve from the "+
				"module proxy at the pinned require versions (sdk de-submodule cutover)", mod)
		}
		for _, m := range contractRequireRe.FindAllStringSubmatch(src, -1) {
			switch m[1] {
			case "sdk":
				if m[2] != wantSDK {
					t.Errorf("%s: requires sdk %s, but the shared sdk pin is %s — run `task mods:tidy`",
						mod, m[2], wantSDK)
				}
			case "spec":
				if m[2] != wantSpec {
					t.Errorf("%s: requires spec %s, but the shared spec pin is %s — run `task mods:tidy`",
						mod, m[2], wantSpec)
				}
			}
		}
	}
}
