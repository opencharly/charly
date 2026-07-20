package main

import (
	"path/filepath"
	"testing"
)

func TestTerminalAgentCandiesExposeGeneratedProfiles(t *testing.T) {
	for _, name := range []string{"claude-code", "codex", "gemini"} {
		manifest := filepath.Join("..", "candy", name, "charly.yml")
		candy, err := parseCandyYAML(manifest)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
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
