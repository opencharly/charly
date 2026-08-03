package loader

// Relocated from charly/cue_entity_xor_test.go (#55 decoupling cone, Batch C):
// TestAndroidDeviceXOR asserts loaderkit.ValidateAndroidDevices directly — zero
// charly coupling beyond a stub resolve-callback (the genuine host coupling the
// compiled-in placement threads; the plugin test stubs it with a direct
// json.Unmarshal since the resolved envelope's JSON shape is wire-identical to
// the authored AndroidSpec body). TestBoxBaseFromXOR_RejectsConflict (a charly
// Config/BoxConfig white-box test) STAYS in charly/cue_entity_xor_test.go.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/sdk/vmshared"
	"github.com/opencharly/spec/spec"
)

// androidTestUF wraps a raw android template map into the PluginKinds shape
// spec.UnifiedFile.Android() reads.
func androidTestUF(m map[string]json.RawMessage) *spec.UnifiedFile {
	return &spec.UnifiedFile{PluginKinds: map[string]map[string]json.RawMessage{"android": m}}
}

// stubResolveAndroid is a test-local stand-in for charly's resolveAndroidViaPlugin
// (which round-trips through the live provider registry): the authored
// spec.AndroidSpec body and the resolved spec.ResolvedAndroid envelope are
// wire-identical (same JSON field names), so a direct decode is equivalent for
// this validator's purposes (it consults only Box/Adb).
func stubResolveAndroid(body json.RawMessage) (*spec.ResolvedAndroid, error) {
	var out spec.ResolvedAndroid
	if len(body) == 0 {
		return &out, nil
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// TestAndroidDeviceXOR proves a kind:android device is rejected unless it sets
// EXACTLY ONE of box: / adb: (the former `#Android & ({box:_}|{adb:_})`
// disjunction — never both, never neither).
func TestAndroidDeviceXOR(t *testing.T) {
	cases := []struct {
		name   string
		spec   spec.AndroidSpec
		reject bool
	}{
		{"box+adb (both) rejected", spec.AndroidSpec{Box: "android-emulator", Adb: &vmshared.AndroidAdbEndpoint{Host: "127.0.0.1:5037"}}, true},
		{"neither rejected", spec.AndroidSpec{Device: "pixel_9a"}, true},
		{"box only ok", spec.AndroidSpec{Box: "android-emulator"}, false},
		{"adb only ok", spec.AndroidSpec{Adb: &vmshared.AndroidAdbEndpoint{Host: "127.0.0.1:5037"}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := tc.spec
			uf := androidTestUF(rawTemplateMap(map[string]*spec.AndroidSpec{"dev": &s}))
			err := loaderkit.ValidateAndroidDevices(uf, stubResolveAndroid)
			if tc.reject {
				if err == nil {
					t.Errorf("validateAndroidDevices accepted an invalid device (should reject)")
				}
			} else if err != nil {
				t.Errorf("validateAndroidDevices rejected a valid device: %v", err)
			}
		})
	}

	// Friendly-message spot-checks (both directions name their failure).
	both := androidTestUF(rawTemplateMap(map[string]*spec.AndroidSpec{"d": {Box: "e", Adb: &vmshared.AndroidAdbEndpoint{Host: "h:1"}}}))
	if err := loaderkit.ValidateAndroidDevices(both, stubResolveAndroid); err == nil || !strings.Contains(err.Error(), "both box: and adb:") {
		t.Errorf("both-source error message: %v", err)
	}
	none := androidTestUF(rawTemplateMap(map[string]*spec.AndroidSpec{"d": {}}))
	if err := loaderkit.ValidateAndroidDevices(none, stubResolveAndroid); err == nil || !strings.Contains(err.Error(), "neither box: nor adb:") {
		t.Errorf("neither-source error message: %v", err)
	}
}
