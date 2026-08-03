package loader

// Relocated from charly/android_loader_test.go (#55 decoupling cone, Batch C):
// TestValidateCheckBeds_Android asserts loaderkit.ValidateCheckBeds directly against
// spec.UnifiedFile fixtures — zero charly coupling. TestLoadUnified_AndroidNodeForm
// (the LoadUnified white-box round-trip) STAYS in charly/android_loader_test.go.

import (
	"encoding/json"
	"testing"

	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/spec/spec"
)

// rawTemplateMap marshals a typed substrate-template map into the opaque
// map[string]json.RawMessage the loader stores after the Cutover I de-type
// (mirrors charly's own test helper of the same name, android_loader_test.go).
func rawTemplateMap[T any](m map[string]*T) map[string]json.RawMessage {
	out := make(map[string]json.RawMessage, len(m))
	for k, v := range m {
		b, _ := json.Marshal(v)
		out[k] = b
	}
	return out
}

// TestValidateCheckBeds_Android covers the kind:check bed validation for a
// top-level target: android bed.
func TestValidateCheckBeds_Android(t *testing.T) {
	// android bed without an android: ref → error.
	uf := &spec.UnifiedFile{
		Bundle: map[string]spec.BundleNode{
			"bed": {Target: "android", Disposable: new(true)},
		},
	}
	if err := loaderkit.ValidateCheckBeds(uf, testThreaded()); err == nil {
		t.Error("target:android bed without android: should fail validation")
	}

	// android bed referencing an undefined device → error.
	uf2 := &spec.UnifiedFile{
		Bundle: map[string]spec.BundleNode{
			"bed": {Target: "android", From: "ghost", Disposable: new(true)},
		},
	}
	if err := loaderkit.ValidateCheckBeds(uf2, testThreaded()); err == nil {
		t.Error("target:android bed referencing an undefined device should fail")
	}

	// android bed referencing a defined device → ok.
	uf3 := &spec.UnifiedFile{
		PluginKinds: map[string]map[string]json.RawMessage{
			"android": rawTemplateMap(map[string]*spec.AndroidSpec{"dev": {Box: "android-emulator"}}),
		},
		Bundle: map[string]spec.BundleNode{
			"bed": {Target: "android", From: "dev", Disposable: new(true)},
		},
	}
	if err := loaderkit.ValidateCheckBeds(uf3, testThreaded()); err != nil {
		t.Errorf("valid target:android bed should pass, got: %v", err)
	}
}
