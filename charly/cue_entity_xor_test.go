package main

// Proof that the THREE entity-level mutual-exclusions that moved OUT of the CUE
// schema (because `cue exp gengotypes` collapses a top-level `& (A|B)`
// disjunction to an empty `struct{}`, defeating the spec drop-in) are STILL
// enforced in Go — runtime validation rejects exactly the same bad configs the
// dropped disjunctions did.
//
//   #Box     base⊻from          → BoxConfig.HasBaseFromConflict / validateBoxBaseFrom (config.go + validate.go)
//   #Android box⊻adb (exactly1) → validateAndroidDevices (unified.go)
//   #Check   bed-mode/target    → validateCheckBeds (unified.go) — proven by the
//                                  existing TestValidateCheckBeds_* suite
//                                  (TargetEnum rejects k8s = the arm's bed-legal
//                                  target ∈ {pod,vm,local,android}; VmRef/LocalRef/
//                                  Android prove the cross-ref + disposable shape).
//                                  No duplicate test here (R3).

import (
	"github.com/opencharly/sdk/spec"
	"strings"
	"testing"
)

// TestBoxBaseFromXOR_RejectsConflict proves a box authoring BOTH base: and from:
// is rejected (the former `#Box & ({from?: _|_} | {base?: _|_})` disjunction),
// while base-only, from-only, and NEITHER (a scratch box — the disjunction's
// "at most one" semantics) are all accepted.
func TestBoxBaseFromXOR_RejectsConflict(t *testing.T) {
	cases := []struct {
		name   string
		box    spec.BoxConfig
		reject bool
	}{
		{"base+from conflict", spec.BoxConfig{Base: "fedora", From: "builder:scratch-builder"}, true},
		{"base only", spec.BoxConfig{Base: "fedora"}, false},
		{"from only", spec.BoxConfig{From: "builder:scratch-builder"}, false},
		{"neither (scratch box)", spec.BoxConfig{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Unit: the shared predicate (one rule, two seams — R3).
			if got := tc.box.HasBaseFromConflict(); got != tc.reject {
				t.Fatalf("HasBaseFromConflict()=%v, want %v", got, tc.reject)
			}
			// Integration: the validate-time surface that collects the error.
			cfg := &Config{Box: boxMapOf(map[string]spec.BoxConfig{"b": tc.box})}
			errs := &ValidationError{}
			validateBoxBaseFrom(cfg, ResolveOpts{}, errs)
			if tc.reject && !errs.HasErrors() {
				t.Errorf("validateBoxBaseFrom accepted a base+from box (should reject)")
			}
			if !tc.reject && errs.HasErrors() {
				t.Errorf("validateBoxBaseFrom rejected a valid box: %v", errs.Error())
			}
		})
	}
}

// TestAndroidDeviceXOR proves a kind:android device is rejected unless it sets
// EXACTLY ONE of box: / adb: (the former `#Android & ({box:_}|{adb:_})`
// disjunction — never both, never neither).
func TestAndroidDeviceXOR(t *testing.T) {
	cases := []struct {
		name   string
		spec   AndroidSpec
		reject bool
	}{
		{"box+adb (both) rejected", AndroidSpec{Box: "android-emulator", Adb: &AndroidAdbEndpoint{Host: "127.0.0.1:5037"}}, true},
		{"neither rejected", AndroidSpec{Device: "pixel_9a"}, true},
		{"box only ok", AndroidSpec{Box: "android-emulator"}, false},
		{"adb only ok", AndroidSpec{Adb: &AndroidAdbEndpoint{Host: "127.0.0.1:5037"}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := tc.spec
			uf := &UnifiedFile{Android: rawTemplateMap(map[string]*AndroidSpec{"dev": &s})}
			err := validateAndroidDevices(uf)
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
	both := &UnifiedFile{Android: rawTemplateMap(map[string]*AndroidSpec{"d": {Box: "e", Adb: &AndroidAdbEndpoint{Host: "h:1"}}})}
	if err := validateAndroidDevices(both); err == nil || !strings.Contains(err.Error(), "both box: and adb:") {
		t.Errorf("both-source error message: %v", err)
	}
	none := &UnifiedFile{Android: rawTemplateMap(map[string]*AndroidSpec{"d": {}})}
	if err := validateAndroidDevices(none); err == nil || !strings.Contains(err.Error(), "neither box: nor adb:") {
		t.Errorf("neither-source error message: %v", err)
	}
}
