package main

import (
	"reflect"
	"testing"

	"github.com/opencharly/sdk/spec"
)

// TestExtractMetadata_EnvObjectLabel proves the ai.opencharly.env label — baked
// as a JSON OBJECT from the image's spec.Box.Env map — parses back into
// meta.Env's []string KEY=VALUE form (sorted, via envMapToPairs). Decoding the
// object straight into []string was the writer/reader mismatch that failed every
// image with a box-level env: map (check box android-emulator: "cannot
// unmarshal object into []string").
func TestExtractMetadata_EnvObjectLabel(t *testing.T) {
	orig := InspectLabels
	defer func() { InspectLabels = orig }()
	InspectLabels = func(engine, imageRef string) (map[string]string, error) {
		return map[string]string{
			spec.LabelVersion: "2026.001.0000",
			spec.LabelBox:     "env-image",
			spec.LabelEnv:     `{"EMULATOR_NAME":"charly_avd_36","EMULATOR_API_LEVEL":"36","EMULATOR_DEVICE":"pixel_9a"}`,
		}, nil
	}
	meta, err := ExtractMetadata("podman", "env-image")
	if err != nil {
		t.Fatalf("ExtractMetadata must decode the env OBJECT label: %v", err)
	}
	want := []string{"EMULATOR_API_LEVEL=36", "EMULATOR_DEVICE=pixel_9a", "EMULATOR_NAME=charly_avd_36"} // sorted
	if !reflect.DeepEqual(meta.Env, want) {
		t.Fatalf("meta.Env: got %v want %v", meta.Env, want)
	}
}

// TestExtractMetadata_EnvAbsent: no env label → nil, no error.
func TestExtractMetadata_EnvAbsent(t *testing.T) {
	orig := InspectLabels
	defer func() { InspectLabels = orig }()
	InspectLabels = func(engine, imageRef string) (map[string]string, error) {
		return map[string]string{spec.LabelVersion: "2026.001.0000", spec.LabelBox: "no-env"}, nil
	}
	meta, err := ExtractMetadata("podman", "no-env")
	if err != nil {
		t.Fatal(err)
	}
	if meta.Env != nil {
		t.Fatalf("expected nil env, got %v", meta.Env)
	}
}
