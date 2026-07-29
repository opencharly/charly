package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/spec/spec"
)

// TestLoadUnified_AndroidNodeForm verifies a unified node-form `android` entity
// loads into loaderkit.UnifiedFile.Android through the standard loader. The legacy
// kind-keyed routing was deleted in the #NodeDoc-sole-gate cutover — node-form is
// the only authoring surface.
func TestLoadUnified_AndroidNodeForm(t *testing.T) {
	dir := t.TempDir()
	doc := `version: "` + latestSchemaVersion.String() + `"
pixel9a-36:
  android:
    box: android-emulator
    device: pixel_9a
    api_level: 36
`
	if err := os.WriteFile(filepath.Join(dir, UnifiedFileName), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	uf, _, err := LoadUnified(dir)
	if err != nil {
		t.Fatalf("LoadUnified(android node-form): %v", err)
	}
	got := lookupAndroidSpec(uf, "pixel9a-36")
	if got == nil {
		t.Fatalf("android node-form entity not registered in uf.Android(); got %v", uf.Android())
	}
	if got.Box != "android-emulator" || got.Device != "pixel_9a" || got.ApiLevel != 36 {
		t.Errorf("android spec round-trip wrong: %+v", got)
	}
}

// rawTemplateMap marshals a typed substrate-template map into the opaque
// map[string]json.RawMessage the loader stores after the Cutover I de-type.
func rawTemplateMap[T any](m map[string]*T) map[string]json.RawMessage {
	out := make(map[string]json.RawMessage, len(m))
	for k, v := range m {
		b, _ := json.Marshal(v)
		out[k] = b
	}
	return out
}

// TestMergeRawTemplateMap relocated to sdk/loaderkit/merge_test.go alongside
// mergeRawTemplateMap (K1-proper — the merge half of the loader moved to loaderkit).

// TestValidateCheckBeds_Android covers the kind:check bed validation for a
// top-level target: android bed.
func TestValidateCheckBeds_Android(t *testing.T) {
	// android bed without an android: ref → error.
	uf := &loaderkit.UnifiedFile{
		Bundle: map[string]spec.BundleNode{
			"bed": {Target: "android", Disposable: new(true)},
		},
	}
	if err := loaderkit.ValidateCheckBeds(uf, loaderThreaded()); err == nil {
		t.Error("target:android bed without android: should fail validation")
	}

	// android bed referencing an undefined device → error.
	uf2 := &loaderkit.UnifiedFile{
		Bundle: map[string]spec.BundleNode{
			"bed": {Target: "android", From: "ghost", Disposable: new(true)},
		},
	}
	if err := loaderkit.ValidateCheckBeds(uf2, loaderThreaded()); err == nil {
		t.Error("target:android bed referencing an undefined device should fail")
	}

	// android bed referencing a defined device → ok.
	uf3 := &loaderkit.UnifiedFile{
		PluginKinds: map[string]map[string]json.RawMessage{
			"android": rawTemplateMap(map[string]*AndroidSpec{"dev": {Box: "android-emulator"}}),
		},
		Bundle: map[string]spec.BundleNode{
			"bed": {Target: "android", From: "dev", Disposable: new(true)},
		},
	}
	if err := loaderkit.ValidateCheckBeds(uf3, loaderThreaded()); err != nil {
		t.Errorf("valid target:android bed should pass, got: %v", err)
	}
}
