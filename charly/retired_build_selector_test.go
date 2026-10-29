package main

// retired_build_selector_test.go — the retired BUILDER SELECTOR (opencharly/charly#1087).
//
// `CHARLY_BUILD_ENGINE` was retired from the contract when the image builder became
// BuildKit: `EngineConfig.Build`, `ResolvedRuntime.BuildEngine` and the
// `CHARLY_BUILD_ENGINE` key are gone from `spec/hostenv`, and a leftover `engine.build`
// is INERT (spec/hostenv/retired_build_selector_test.go pins that). Setting the variable
// now changes nothing.
//
// The Go-level arms pin the CONTRACT. This file pins the SURFACE: the retired knob's NAME
// must not survive in this repo's own example/config surfaces, where a user copies it into
// `~/.config/charly/.env` and expects it to do something. `.env.example` is hand-maintained
// — nothing rewrites it when a knob dies — and the line DID survive a green gate, which is
// this file's reason to exist.
//
// The arm is CLAIM-KEYED (the retired name itself), not keyword-shaped: a comment, an
// example line and a config key are the same finding, because a user reads all three the
// same way. The name may survive in exactly two kinds of place — HISTORY (`CHANGELOG/`),
// and the files that NARRATE the retirement. Everywhere else it is a finding.
//
// The sweep reads the repo's TRACKED files, and it derives that set from the INDEX —
// the same rule `scripts/check-pin-verb-scope.py` applies, so this repo has ONE notion
// of "the tracked files" (R2). It reads each tracked path's CONTENT from disk, so a
// change that is not staged yet is still judged; the index decides WHICH files are in
// scope, and the working tree decides WHAT they say.

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// retiredBuildEngineName is the retired knob's ENV NAME — the spelling a user would set.
const retiredBuildEngineName = "CHARLY_BUILD_ENGINE"

// retiredBuildEngineNarration lists the tracked files that may NAME the retired knob, each
// with the reason it may. A file not in this list, and not under CHANGELOG/, is a finding.
var retiredBuildEngineNarration = map[string]string{
	"charly/runtime_config_test.go":         "the test that narrates the retirement",
	"charly/retired_build_selector_test.go": "this control",
}

// repoRootFromModule returns the repository root. The package dir IS the module root
// (`<repo>/charly/`), so the repo root is its parent — the same anchor `rootCharlyYML`
// uses for the repo's own manifest.
func repoRootFromModule(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve the repo root from the module dir: %v", err)
	}
	return root
}

// trackedRegularFiles returns the repository's tracked REGULAR files, as paths relative
// to the repo root — derived from the INDEX, never hand-kept. A gitlink (160000) and a
// symlink (120000) are skipped: neither is a file this repo's prose lives in.
//
// The set comes from `git ls-files -s -z`, the same enumeration
// `scripts/check-pin-verb-scope.py` uses, so this repo has ONE definition of "the
// tracked files" (R2). Deriving it from the index also means the sweep's scope is the
// COMMITTED surface: an untracked scratch file in a developer's working tree is not
// this repo's surface and must not fail this gate.
func trackedRegularFiles(t *testing.T, root string) []string {
	t.Helper()
	cmd := exec.Command("git", "ls-files", "-s", "-z")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("read the index of %s: %v", root, err)
	}
	var files []string
	for _, rec := range strings.Split(string(out), "\x00") {
		if rec == "" {
			continue
		}
		tab := strings.IndexByte(rec, '\t')
		if tab == -1 {
			continue
		}
		mode := strings.Fields(rec[:tab])[0]
		if mode == "160000" || mode == "120000" {
			continue
		}
		files = append(files, filepath.FromSlash(rec[tab+1:]))
	}
	return files
}

// TestRetiredBuildSelectorNameSurvivesOnlyInHistoryAndNarration — the retired knob's name
// may survive only in history and in the files that narrate the retirement. Every other
// tracked file is a finding: a surface that still offers (or names) a knob the contract
// retired teaches a user to set something that does nothing.
func TestRetiredBuildSelectorNameSurvivesOnlyInHistoryAndNarration(t *testing.T) {
	root := repoRootFromModule(t)

	visited := 0
	for _, rel := range trackedRegularFiles(t, root) {
		path := filepath.Join(root, rel)
		info, infoErr := os.Stat(path)
		if infoErr != nil || !info.Mode().IsRegular() {
			continue // a tracked path that is not a readable regular file
		}
		if info.Size() > 1<<20 {
			continue // a large blob
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil || bytes.IndexByte(data, 0) >= 0 {
			continue // binary
		}
		visited++
		if !strings.Contains(string(data), retiredBuildEngineName) {
			continue
		}
		slashed := filepath.ToSlash(rel)
		if slashed == "CHANGELOG" || strings.HasPrefix(slashed, "CHANGELOG/") {
			continue // history
		}
		if reason, ok := retiredBuildEngineNarration[slashed]; ok {
			t.Logf("%s names the retired knob — allowed: %s", slashed, reason)
			continue
		}
		t.Errorf("%s names the retired knob %q: the contract carries no builder selector, so setting it does nothing",
			slashed, retiredBuildEngineName)
	}
	if visited < 100 {
		t.Fatalf("the sweep read %d file(s) under %s — the sweep is too small to have covered the repo's surfaces", visited, root)
	}
	t.Logf("read %d tracked file(s) under %s", visited, root)
}

// TestEnvExampleOffersOnlyLiveKnobs — the example file is the surface a user copies, so it
// must offer LIVE knobs. `CHARLY_RUN_ENGINE` is one: the run engine still resolves
// (TestResolveRuntime_DockerRunEngineResolvesDirect), so the example keeps it. This arm
// also proves the sweep above is not vacuous: it reads the file the finding is about.
func TestEnvExampleOffersOnlyLiveKnobs(t *testing.T) {
	example := filepath.Join(repoRootFromModule(t), ".env.example")
	data, err := os.ReadFile(example)
	if err != nil {
		t.Fatalf("read %s: %v", example, err)
	}
	text := string(data)
	if !strings.Contains(text, "CHARLY_RUN_ENGINE") {
		t.Errorf("%s no longer offers CHARLY_RUN_ENGINE — the run engine is still live, so the example must keep it", example)
	}
	if strings.Contains(text, retiredBuildEngineName) {
		t.Errorf("%s still offers the retired knob %q", example, retiredBuildEngineName)
	}
}
