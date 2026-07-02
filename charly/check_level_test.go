package main

import "testing"

// The ladder ordering + default are load-bearing for the bed-runner depth
// dispatch — a regression here would silently change how deep every bed runs.
func TestCheckLevelReaches(t *testing.T) {
	cases := []struct {
		have, want string
		reaches    bool
	}{
		{CheckLevelNone, CheckLevelBuild, false},
		{CheckLevelBuild, CheckLevelBuild, true},
		{CheckLevelBuild, CheckLevelNoAgent, false},
		{CheckLevelNoAgent, CheckLevelNoAgent, true},
		{CheckLevelNoAgent, CheckLevelAgent, false},
		{CheckLevelAgent, CheckLevelAgent, true},
		{CheckLevelAgent, CheckLevelBuild, true},
		{"", CheckLevelNoAgent, true}, // empty resolves to the noagent default
		{"", CheckLevelAgent, false},  // ...which does NOT reach agent
		{"", CheckLevelBuild, true},   // ...but does reach build
	}
	for _, c := range cases {
		if got := CheckLevelReaches(c.have, c.want); got != c.reaches {
			t.Errorf("CheckLevelReaches(%q, %q) = %v, want %v", c.have, c.want, got, c.reaches)
		}
	}
}

func TestResolveCheckLevel_DefaultsToNoAgent(t *testing.T) {
	if got := ResolveCheckLevel(""); got != CheckLevelNoAgent {
		t.Errorf("ResolveCheckLevel(\"\") = %q, want %q", got, CheckLevelNoAgent)
	}
	if got := ResolveCheckLevel("agent"); got != CheckLevelAgent {
		t.Errorf("ResolveCheckLevel(\"agent\") = %q, want %q", got, CheckLevelAgent)
	}
}

func TestIsValidCheckLevel(t *testing.T) {
	for _, ok := range []string{"none", "build", "noagent", "agent"} {
		if !IsValidCheckLevel(ok) {
			t.Errorf("IsValidCheckLevel(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{"", "full", "deploy", "AGENT"} {
		if IsValidCheckLevel(bad) {
			t.Errorf("IsValidCheckLevel(%q) = true, want false", bad)
		}
	}
}

// The check_level capability label must round-trip: emitted from BoxConfig at
// build (normalized via ResolveCheckLevel), parsed back into BoxMetadata at
// deploy. Mirrors the deleted LabelCheck round-trip coverage.
func TestExtractMetadata_CheckLevel(t *testing.T) {
	orig := InspectLabels
	defer func() { InspectLabels = orig }()

	InspectLabels = func(engine, imageRef string) (map[string]string, error) {
		return map[string]string{
			LabelVersion:    "1",
			LabelBox:        "x",
			LabelCheckLevel: "agent",
		}, nil
	}
	meta, err := ExtractMetadata("podman", "x")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if meta.CheckLevel != "agent" {
		t.Errorf("meta.CheckLevel = %q, want agent", meta.CheckLevel)
	}
}

// validateBuild rejects an out-of-ladder check_level value.
// check_level enum rejection is now a CUE concern (#Box.check_level) — see
// TestCueTightening_RejectsAndAccepts "box bad check_level rejected".
