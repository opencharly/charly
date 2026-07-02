package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoadUnified_AndroidNodeForm verifies a unified node-form `android` entity
// loads into UnifiedFile.Android through the standard loader. The legacy
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
	got := uf.Android["pixel9a-36"]
	if got == nil {
		t.Fatalf("android node-form entity not registered in uf.Android; got %v", uf.Android)
	}
	if got.Box != "android-emulator" || got.Device != "pixel_9a" || got.ApiLevel != 36 {
		t.Errorf("android spec round-trip wrong: %+v", got)
	}
}

// TestMergeAndroidMap verifies the root-wins merge semantics for android: maps.
func TestMergeAndroidMap(t *testing.T) {
	dst := map[string]*AndroidSpec{"a": {Box: "keep"}}
	src := map[string]*AndroidSpec{"a": {Box: "drop"}, "b": {Box: "add"}}
	mergeAndroidMap(&dst, src)
	if dst["a"].Box != "keep" {
		t.Errorf("existing entry should win: got %q", dst["a"].Box)
	}
	if dst["b"] == nil || dst["b"].Box != "add" {
		t.Errorf("new entry should be added: %+v", dst["b"])
	}
}

// TestValidateCheckBeds_Android covers the kind:check bed validation for a
// top-level target: android bed.
func TestValidateCheckBeds_Android(t *testing.T) {
	// android bed without an android: ref → error.
	uf := &UnifiedFile{
		Bundle: map[string]BundleNode{
			"bed": {Target: "android", Disposable: new(true)},
		},
	}
	if err := validateCheckBeds(uf); err == nil {
		t.Error("target:android bed without android: should fail validation")
	}

	// android bed referencing an undefined device → error.
	uf2 := &UnifiedFile{
		Bundle: map[string]BundleNode{
			"bed": {Target: "android", From: "ghost", Disposable: new(true)},
		},
	}
	if err := validateCheckBeds(uf2); err == nil {
		t.Error("target:android bed referencing an undefined device should fail")
	}

	// android bed referencing a defined device → ok.
	uf3 := &UnifiedFile{
		Android: map[string]*AndroidSpec{"dev": {Box: "android-emulator"}},
		Bundle: map[string]BundleNode{
			"bed": {Target: "android", From: "dev", Disposable: new(true)},
		},
	}
	if err := validateCheckBeds(uf3); err != nil {
		t.Errorf("valid target:android bed should pass, got: %v", err)
	}
}
