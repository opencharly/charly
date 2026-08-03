package main

// Proof that the THREE entity-level mutual-exclusions that moved OUT of the CUE
// schema (because `cue exp gengotypes` collapses a top-level `& (A|B)`
// disjunction to an empty `struct{}`, defeating the spec drop-in) are STILL
// enforced in Go — runtime validation rejects exactly the same bad configs the
// dropped disjunctions did.
//
//   #Box     base⊻from          → BoxConfig.HasBaseFromConflict / validateBoxBaseFrom (config.go + validate.go)
//   #Android box⊻adb (exactly1) → loaderkit.ValidateAndroidDevices — relocated to
//                                  candy/plugin-loader/cue_entity_xor_test.go (#55
//                                  decoupling cone, Batch C; TestAndroidDeviceXOR)
//   #Check   bed-mode/target    → validateCheckBeds (unified.go) — proven by the
//                                  existing TestValidateCheckBeds_* suite
//                                  (TargetEnum rejects k8s = the arm's bed-legal
//                                  target ∈ {pod,vm,local,android}; VmRef/LocalRef/
//                                  Android prove the cross-ref + disposable shape).
//                                  No duplicate test here (R3).

import (
	"testing"

	"github.com/opencharly/spec/spec"
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
			cfg := &spec.Config{Box: boxMapOf(map[string]spec.BoxConfig{"b": tc.box})}
			errs := &spec.ValidationError{}
			validateBoxBaseFrom(cfg, spec.ResolveOpts{}, errs)
			if tc.reject && !errs.HasErrors() {
				t.Errorf("validateBoxBaseFrom accepted a base+from box (should reject)")
			}
			if !tc.reject && errs.HasErrors() {
				t.Errorf("validateBoxBaseFrom rejected a valid box: %v", errs.Error())
			}
		})
	}
}

// TestAndroidDeviceXOR relocated to
// candy/plugin-loader/cue_entity_xor_test.go (#55 decoupling cone, Batch C) —
// it asserted loaderkit.ValidateAndroidDevices directly, zero charly coupling
// beyond a stub resolve callback.
