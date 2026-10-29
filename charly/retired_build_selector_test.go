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

import (
	"bytes"
	"io/fs"
	"os"
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

// TestRetiredBuildSelectorNameSurvivesOnlyInHistoryAndNarration — the retired knob's name
// may survive only in history and in the files that narrate the retirement. Every other
// tracked file is a finding: a surface that still offers (or names) a knob the contract
// retired teaches a user to set something that does nothing.
func TestRetiredBuildSelectorNameSurvivesOnlyInHistoryAndNarration(t *testing.T) {
	root := repoRootFromModule(t)

	visited := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil // an unreadable entry is not a finding
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)

		if d.IsDir() {
			switch d.Name() {
			case ".git", "bin", "node_modules":
				return fs.SkipDir
			}
			if path != root {
				// A nested repository (a gitlink's checkout) is a repo of its own, with its
				// own rulebook and its own controls. This control speaks for THIS repo.
				if _, statErr := os.Stat(filepath.Join(path, ".git")); statErr == nil {
					return fs.SkipDir
				}
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil || info.Size() > 1<<20 {
			return nil // a build artifact or a large blob
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil || bytes.IndexByte(data, 0) >= 0 {
			return nil // binary
		}
		visited++
		if !strings.Contains(string(data), retiredBuildEngineName) {
			return nil
		}
		if rel == "CHANGELOG" || strings.HasPrefix(rel, "CHANGELOG/") {
			return nil // history
		}
		if reason, ok := retiredBuildEngineNarration[rel]; ok {
			t.Logf("%s names the retired knob — allowed: %s", rel, reason)
			return nil
		}
		t.Errorf("%s names the retired knob %q: the contract carries no builder selector, so setting it does nothing",
			rel, retiredBuildEngineName)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	if visited < 100 {
		t.Fatalf("the walk read %d file(s) under %s — the sweep is too small to have covered the repo's surfaces", visited, root)
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
