package gomodcanonical

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

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
	// Look each contract up BY NAME. Taking the FIRST match silently anchored on `sdk`
	// once sdk became an INDIRECT require of core and sorted above spec in go.mod — at
	// which point this test fatals on a tree whose pins are entirely correct.
	corePins := map[string]string{}
	for _, m := range contractRequireRe.FindAllStringSubmatch(string(cb), -1) {
		corePins[m[1]] = m[2]
	}
	wantSpec, okSpec := corePins["spec"]
	if !okSpec {
		t.Fatalf("%s: no `github.com/opencharly/spec <version>` require found — the core module "+
			"is the shared-spec-pin anchor", coreMod)
	}

	// The sdk pin WAS a hardcoded constant, on the premise that core imports zero sdk
	// packages and so offers no anchor. Core now carries an sdk require (indirect, via
	// the compiled-in plugin modules), so anchor it exactly as spec is anchored. The
	// constant had already drifted from the tree it polices (v0.2026234.347 against the
	// tree's v0.2026241.1032) — the argument against a hand-maintained copy of a value
	// that already exists in the tree.
	wantSDK, okSDK := corePins["sdk"]
	if !okSDK {
		t.Fatalf("%s: no `github.com/opencharly/sdk <version>` require found — the core module "+
			"is the shared-sdk-pin anchor", coreMod)
	}

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
