package main

// kind_value_composed_test.go proves the TWO halves of the kind-value gate's composition
// cutover (TODO 2.1 unit 0, spec#304):
//
//  1. the gate resolves the #<Kind>Value def its PROVIDER declares — following the shape the
//     provider declares instead of a host-side kind list;
//  2. the def is read from the COMPOSED unit (base & every served plugin unit), so a kind body
//     that has moved into its owning plugin's CUE module is visible to the gate;
//  3. for every kind whose body still lives in the base, the composed value is EQUAL to the base
//     — the measurement that makes the change behaviour-preserving today, pinned so it cannot
//     silently stop being true.
//
// The first two are proved with a DEF the base does not declare: the test registers a plugin
// unit that serves `#GateprobeValue`, asserts the base cannot see it, and shows the gate both
// ACCEPTING a conforming body and REJECTING a typo'd field through it. Without the composed
// lookup the gate would fail with "kind value def #GateprobeValue not found"; without the
// provider-declared path the node would not be gated at all.

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"cuelang.org/go/cue"

	"github.com/opencharly/spec/spec"
)

// gateProbeProvider is a Provider that only carries capability metadata — the gate never
// invokes it, so Invoke fails loudly if the test (or the code under test) ever tries.
type gateProbeProvider struct{ capMeta }

func (p gateProbeProvider) Invoke(context.Context, *Operation) (*Result, error) {
	return nil, fmt.Errorf("gateProbeProvider: not invocable")
}

// gateProbeUnit registers a plugin unit whose served schema declares a host-value def that the
// BASE does not have, and returns the provider that DECLARES it (class:kind, InputDef
// #GateprobeValue) — the shape a kind body declares once it has moved into its plugin.
func gateProbeUnit(t *testing.T) Provider {
	t.Helper()
	const src = `
#GateprobeValue: {
	label: string
	count?: int
}
`
	schema := PluginSchema{
		CueSource: src,
		InputDefs: map[string]string{provKey(ClassKind, "gateprobe"): "#GateprobeValue"},
	}
	if err := registerPluginUnitSchema("gateprobe", schema); err != nil {
		t.Fatalf("register the gateprobe unit: %v", err)
	}
	return gateProbeProvider{capMeta: capMeta{class: ClassKind, word: "gateprobe", inputDef: "#GateprobeValue"}}
}

func gateProbeNode(t *testing.T, body string) spec.ParsedNode {
	t.Helper()
	return spec.ParsedNode{Name: "probe", Disc: "gateprobe", Body: spec.RawBody(body)}
}

// TestKindValueGate_ReadsAPluginDeclaredDefFromTheComposedUnit is the R7 witness for the new
// path: with the def declared ONLY by a registered plugin unit, the gate validates the authored
// body against it (accept conforming, reject a typo) — and the base provably cannot serve it.
func TestKindValueGate_ReadsAPluginDeclaredDefFromTheComposedUnit(t *testing.T) {
	prov := gateProbeUnit(t)

	// The base alone does NOT carry the def — so a pass below can only come from the composed
	// unit, never from the fallback.
	if d := sharedCueSchema().LookupPath(cue.ParsePath("#GateprobeValue")); d.Err() == nil {
		t.Fatal("the BASE already declares #GateprobeValue — this fixture no longer discriminates")
	}

	// The provider-declared shape is what selects the host-value gate (not a host kind list).
	if def, ok := kindValueDefPath(prov, "gateprobe"); !ok || def != "#GateprobeValue" {
		t.Fatalf("kindValueDefPath(provider-declared) = %q,%v; want #GateprobeValue,true", def, ok)
	}

	// ACCEPT: a conforming body validates through the plugin-declared, composed def.
	if err := validateKindValueCUE(gateProbeNode(t, `{"label":"ok"}`), prov); err != nil {
		t.Fatalf("a conforming gateprobe body must pass the plugin-declared def: %v", err)
	}

	// REJECT: a typo'd field is a hard load error raised BY that def — the closedness the gate
	// exists for, now carried by a plugin instead of the base.
	err := validateKindValueCUE(gateProbeNode(t, `{"label":"ok","bogus":1}`), prov)
	if err == nil {
		t.Fatal("a typo'd gateprobe field must be rejected by the plugin-declared def")
	}
	if !strings.Contains(err.Error(), "bogus") || !strings.Contains(err.Error(), "gateprobe") {
		t.Fatalf("rejection must name the field and the kind, got: %v", err)
	}
}

// TestKindValueDefPath_FollowsTheProviderDeclaredShape pins the ROUTING contract: a kind
// capability naming a `#<Word>Value` body def is host-value-gated, while a flat plugin kind
// naming its self-contained `#<Word>Input` is NOT — the provider's declaration, never a host
// kind-word switch, decides. A nil provider keeps the compatibility fallback for every kind
// whose body still lives in the base.
func TestKindValueDefPath_FollowsTheProviderDeclaredShape(t *testing.T) {
	// A kind that is NOT in the base's map must still be host-value-gated when its provider
	// declares a `#<Word>Value` body def — the provider's declaration is what decides, so this
	// assertion has teeth against a regression that keeps only the map fallback.
	declared := gateProbeProvider{capMeta: capMeta{class: ClassKind, word: "declaredprobe", inputDef: "#DeclaredprobeValue"}}
	if def, gated := kindValueDefPath(declared, "declaredprobe"); !gated || def != "#DeclaredprobeValue" {
		t.Fatalf("a provider declaring a #<Word>Value body def must be host-value-gated; got %q,%v", def, gated)
	}

	flat := gateProbeProvider{capMeta: capMeta{class: ClassKind, word: "flatprobe", inputDef: "#FlatprobeInput"}}
	if def, gated := kindValueDefPath(flat, "flatprobe"); gated {
		t.Fatalf("a flat plugin kind declaring %q must NOT be host-value-gated (got %q)", "#FlatprobeInput", def)
	}

	deployArm := gateProbeProvider{capMeta: capMeta{class: ClassKind, word: "armprobe", inputDef: "#DeployValue"}}
	if _, gated := kindValueDefPath(deployArm, "armprobe"); gated {
		t.Fatal("the shared #DeployValue disjunct is not a kind body def and must not be gated as one")
	}

	if def, ok := kindValueDefPath(nil, "task"); !ok || def != spec.KindValueDefs["task"] {
		t.Fatalf("nil provider must fall back to the base map for an unmoved kind; got %q,%v", def, ok)
	}

	if _, ok := kindValueDefPath(nil, "agent"); ok {
		t.Fatal("a kind the base does not value-gate must stay un-gated for a nil provider")
	}
}

// TestKindValueDefs_ComposedEqualsBase is the behaviour-preservation MEASUREMENT, made durable:
// for every kind still declared by the base, the def the gate now reads from the composed unit
// is EQUAL to the one it read before from the base alone. If a future plugin unit shadows one of
// these defs, this fails rather than silently changing what the gate accepts.
func TestKindValueDefs_ComposedEqualsBase(t *testing.T) {
	composed, ok := composedSchema()
	if !ok {
		t.Fatal("no plugin unit schema is composed — TestMain must register the builtin units")
	}
	if len(spec.KindValueDefs) == 0 {
		t.Fatal("the base declares no kind value defs — the derivation changed")
	}
	for disc, defPath := range spec.KindValueDefs {
		baseDef := sharedCueSchema().LookupPath(cue.ParsePath(defPath))
		if baseDef.Err() != nil {
			t.Errorf("%s: base does not declare %s: %v", disc, defPath, baseDef.Err())
			continue
		}
		composedDef := composed.LookupPath(cue.ParsePath(defPath))
		if composedDef.Err() != nil {
			t.Errorf("%s: composed unit does not declare %s: %v", disc, defPath, composedDef.Err())
			continue
		}
		if !baseDef.Equals(composedDef) {
			t.Errorf("%s: the composed %s is not EQUAL to the base's — the gate's verdicts can no longer be assumed unchanged", disc, defPath)
			continue
		}
		t.Logf("%s %s: composed == base", disc, defPath)
	}
}
