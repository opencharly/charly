package main

import "testing"

// The check_level capability label must round-trip: emitted from BoxConfig at build
// (normalized via ResolveCheckLevel, now kit-sourced), parsed back into BoxMetadata at deploy.
// Stays in core because it exercises ExtractMetadata + the OCI label surface, not the ladder
// logic (that moved to sdk/kit with its own tests).
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
