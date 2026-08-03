package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/opencharly/spec/spec"
)

// TestLoadUnified_AndroidNodeForm verifies a unified node-form `android` entity
// loads into spec.UnifiedFile.Android through the standard loader. The legacy
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
	if err := os.WriteFile(filepath.Join(dir, spec.UnifiedFileName), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	uf, _, err := LoadUnified(dir)
	if err != nil {
		t.Fatalf("LoadUnified(android node-form): %v", err)
	}
	body, ok := uf.Android()["pixel9a-36"]
	if !ok {
		t.Fatalf("android node-form entity not registered in uf.Android(); got %v", uf.Android())
	}
	got, err := resolveAndroidViaPlugin(body)
	if err != nil {
		t.Fatalf("resolveAndroidViaPlugin: %v", err)
	}
	if got == nil {
		t.Fatal("resolveAndroidViaPlugin returned a nil spec")
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

// TestValidateCheckBeds_Android relocated to
// candy/plugin-loader/android_loader_test.go (#55 decoupling cone, Batch C) —
// it asserted loaderkit.ValidateCheckBeds directly, zero charly coupling.
