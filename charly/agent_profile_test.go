package main

import (
	"path/filepath"
	"testing"
)

// TestTerminalAgentCandiesExposeGeneratedProfiles asserts the claude-code/codex terminal
// profiles + agent_provide wiring survive the candy de-submodule cutover (Phase 4): the in-repo
// candy dirs are deleted, so the manifests are resolved from the STANDALONE repos via the
// canonical loader fetch (requireProjectLoader().EnsureRepoDownloaded — the same fetch the
// runtime scan uses), and parsed with the same parseCandyYAML seam.
func TestTerminalAgentCandiesExposeGeneratedProfiles(t *testing.T) {
	for _, name := range []string{"claude-code", "codex"} {
		repo := "layer-" + name
		tag := "v2026.237.456"
		if name == "codex" {
			tag = "v2026.237.556"
		}
		dir, err := requireProjectLoader().EnsureRepoDownloaded(hostInProcCtx(), "github.com/opencharly/"+repo, tag)
		if err != nil {
			t.Fatalf("%s: fetch %s@%s: %v", name, repo, tag, err)
		}
		manifest := filepath.Join(dir, "charly.yml")
		candy, err := parseCandyYAML(manifest)
		if err != nil {
			t.Fatalf("%s: parse %s: %v", name, manifest, err)
		}
		profile, ok := candy.TerminalProfiles[name]
		if !ok {
			t.Fatalf("%s: terminal profile missing", name)
		}
		if profile.Name != name || len(profile.Entrypoint) == 0 || profile.Persistence != "required" || profile.Transcript != "both" {
			t.Errorf("%s: incomplete profile: %#v", name, profile)
		}
		if len(candy.AgentProvide) != 1 || len(candy.AgentProvide[0].Profiles) != 1 || candy.AgentProvide[0].Profiles[0] != name {
			t.Errorf("%s: agent_provide does not reference profile", name)
		}
	}
}
