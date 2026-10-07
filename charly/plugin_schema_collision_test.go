package main

import (
	"strings"
	"testing"
)

// plugin_schema_collision_test.go — the acceptance tests for charly#770: a plugin-vs-plugin CUE
// def-name collision must make the LOAD GATE fail loudly naming BOTH plugins and the def, instead
// of CUE silently unifying the two shapes into one.
//
// WHY THIS FILE EXISTS (MEASURED, RDD spikes in plan/progress/loader-paths.md): CUE unifies
// same-name fields across sources in one package, so before the gate, two plugins declaring #X
// registered with NO error (spike 1) — and then BOTH rejected their own authored input, because the
// merged #X required the other plugin's field too. The gate is keyed on DECLARATION IDENTITY, never
// on a value comparison: spike 5 MEASURED that unifying plugin-matching's own
// `(#MatchingMatcher | [...#MatchingMatcher])` with ITSELF yields a value that is not `Equals` to
// the original and is strictly LOOSER, so "is this merge a no-op?" is not a decidable question.
//
// The real-corpus arm is TestMain: it runs loadBuiltinPluginUnits() (all 63 builtin plugin schemas)
// and os.Exit(1)s on any gate error — so every `go test` run on this package proves the gate accepts
// the shipped corpus, including the documented self-contained wire twin
// (spec/schema/tunnel.cue ↔ plugin-tunnel/schema/tunnel.cue: #TunnelConfig / #TunnelPort).

// TestPluginSchemaDefNameCollisionErrors is charly#770's core assertion: two plugins declaring one
// def name is a hard error naming both plugins and the def, the rejected plugin leaves NO state
// behind, and the first plugin still validates its own input (the "breaks BOTH plugins" half of the
// defect).
func TestPluginSchemaDefNameCollisionErrors(t *testing.T) {
	t.Cleanup(snapshotProviderState())

	const def = "#CollideGateInput"
	a := PluginSchema{
		CueSource: def + ": {\n\talpha: string\n}\n",
		InputDefs: map[string]string{"verb:collidegatealpha": def},
	}
	b := PluginSchema{
		CueSource: def + ": {\n\tbeta: string\n}\n",
		InputDefs: map[string]string{"verb:collidegatebeta": def},
	}
	if err := registerPluginUnitSchema("collide-gate-plugin-a", a); err != nil {
		t.Fatalf("plugin A alone must register: %v", err)
	}
	if err := validateAuthoredPluginInput(ClassVerb, "collidegatealpha", []byte(`{"alpha":"x"}`)); err != nil {
		t.Fatalf("plugin A must validate its own input before the collision: %v", err)
	}

	err := registerPluginUnitSchema("collide-gate-plugin-b", b)
	if err == nil {
		t.Fatalf("a second plugin declaring %s MUST fail the load gate — CUE would otherwise unify the two shapes silently and break both plugins (charly#770)", def)
	}
	for _, want := range []string{"collide-gate-plugin-b", "collide-gate-plugin-a", def} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("collision error must name %q (the gate must name BOTH plugins and the def); got: %v", want, err)
		}
	}

	// The rejected registration must have changed NOTHING: plugin A still validates its own input,
	// and B's input def was never committed.
	if err := validateAuthoredPluginInput(ClassVerb, "collidegatealpha", []byte(`{"alpha":"x"}`)); err != nil {
		t.Errorf("plugin A's own input must still validate after B was rejected: %v", err)
	}
	if _, ok := pluginSchemas.inputDefs[provKey(ClassVerb, "collidegatebeta")]; ok {
		t.Error("the rejected plugin's input def must NOT be registered")
	}
	pluginSchemas.mu.Lock()
	owner, owned := pluginSchemas.defOwners[def]
	pluginSchemas.mu.Unlock()
	if !owned || owner.name != "collide-gate-plugin-a" {
		t.Errorf("declaration index must still name only plugin A for %s; got owned=%v owner=%q", def, owned, owner.name)
	}
}

// TestPluginSchemaSameShapeDifferentTextStillErrors pins that the gate is IDENTITY-based, not
// value-based: a second plugin declaring a def name whose body is SEMANTICALLY IDENTICAL but whose
// served source text differs (here: one added comment) is still a hard error, because the two
// declarations are two sources in one CUE package and nothing in the merged value can tell that
// apart from a real shape change (spike 5: a self-unification is already not Equals-equal there).
// The fix for a duplicate name is to rename the def (the documented `#<Word>Input` convention); the
// legitimately re-served SAME bytes are a replay and ARE allowed
// (TestPluginSchemaReplayIsIdempotent).
func TestPluginSchemaSameShapeDifferentTextStillErrors(t *testing.T) {
	t.Cleanup(snapshotProviderState())

	const def = "#CollideShapeInput"
	body := "\tmarker?: string\n\tcount?: int\n"
	if err := registerPluginUnitSchema("collide-shape-plugin-a", PluginSchema{
		CueSource: def + ": {\n" + body + "}\n",
		InputDefs: map[string]string{"verb:collideshapea": def},
	}); err != nil {
		t.Fatalf("first plugin must register: %v", err)
	}
	err := registerPluginUnitSchema("collide-shape-plugin-b", PluginSchema{
		// The SAME shape, a different served source text (one comment).
		CueSource: "// plugin B's own copy of the same def name\n" + def + ": {\n" + body + "}\n",
		InputDefs: map[string]string{"verb:collideshapeb": def},
	})
	if err == nil {
		t.Fatalf("a second plugin declaring %s — even with a semantically identical body — must be a hard error (declaration identity, not value equality)", def)
	}
	if !strings.Contains(err.Error(), "collide-shape-plugin-a") {
		t.Errorf("the error must name the first declarer; got: %v", err)
	}
}

// TestPluginSchemaReplayIsIdempotent pins the ONE allowance: re-registering the EXACT same served
// source string is a replay, not a second declaration. It is load-bearing in production (the
// builtin schema gate re-run from a cleared state, and the embedded-defaults path re-registering a
// unit) and it is why the `sources` slice no longer grows under `-count>N`.
func TestPluginSchemaReplayIsIdempotent(t *testing.T) {
	t.Cleanup(snapshotProviderState())

	schema := PluginSchema{
		CueSource: "#CollideReplayInput: {\n\tmarker?: string\n}\n",
		InputDefs: map[string]string{"verb:collidereplay": "#CollideReplayInput"},
	}
	if err := registerPluginUnitSchema("collide-replay-plugin", schema); err != nil {
		t.Fatalf("first registration must succeed: %v", err)
	}
	pluginSchemas.mu.Lock()
	afterFirst := len(pluginSchemas.sources)
	pluginSchemas.mu.Unlock()

	if err := registerPluginUnitSchema("collide-replay-plugin", schema); err != nil {
		t.Fatalf("re-registering the SAME source string is a replay and must succeed: %v", err)
	}
	pluginSchemas.mu.Lock()
	afterReplay := len(pluginSchemas.sources)
	pluginSchemas.mu.Unlock()
	if afterReplay != afterFirst {
		t.Errorf("a replay must not re-append the source (sources %d → %d)", afterFirst, afterReplay)
	}
	if err := validateAuthoredPluginInput(ClassVerb, "collidereplay", []byte(`{"marker":"m"}`)); err != nil {
		t.Errorf("the replayed plugin must still validate its own input: %v", err)
	}
}

// TestPluginSchemaBaseDefTwinAccepted pins the deliberate NON-goal: a plugin schema declaring a def
// name the BASE also declares is accepted. A plugin schema must be self-contained (it may reference
// no base def), so a host-side WIRE twin is a structural necessity of that contract — the live
// instance is spec/schema/tunnel.cue's #TunnelConfig/#TunnelPort, deliberately twinned in
// candy/plugin-tunnel/schema/tunnel.cue and documented there as the established host-call wire
// convention. Only plugin-vs-plugin collisions are the gate's verdict; TestMain already exercises
// the real corpus twin on every run.
func TestPluginSchemaBaseDefTwinAccepted(t *testing.T) {
	t.Cleanup(snapshotProviderState())

	baseV, err := compileBasePlusServed("")
	if err != nil {
		t.Fatal(err)
	}
	baseDefs, err := topLevelDefNames(baseV)
	if err != nil {
		t.Fatal(err)
	}
	if len(baseDefs) == 0 {
		t.Fatal("the base schema declares no defs — this test cannot pin the twin allowance")
	}
	// Re-declare the first base def (whatever it is) under a plugin's own schema.
	twin := baseDefs[0] + ": _\n"
	if err := registerPluginUnitSchema("collide-base-twin-plugin", PluginSchema{
		CueSource: twin,
	}); err != nil {
		t.Fatalf("a plugin re-declaring a BASE def (%s) is the documented self-contained wire twin and must be accepted: %v", baseDefs[0], err)
	}
}

// TestPluginSchemaMustCompileStandalone pins the self-containment precondition the collision gate
// rests on (and that the plugin skill documents): a schema that only compiles once spliced onto the
// base has def names the gate cannot read per source, so it is a hard error — never a silent skip.
func TestPluginSchemaMustCompileStandalone(t *testing.T) {
	t.Cleanup(snapshotProviderState())

	err := registerPluginUnitSchema("collide-gate-base-referencing", PluginSchema{
		CueSource: "#CollideGateRefersBase: {\n\tstep: #Step\n}\n",
		InputDefs: map[string]string{"verb:collidegateref": "#CollideGateRefersBase"},
	})
	if err == nil {
		t.Fatal("a schema referencing a base def must be rejected: a plugin schema is self-contained (it must compile standalone)")
	}
	if !strings.Contains(err.Error(), "STANDALONE") {
		t.Errorf("the error must name the standalone-compile precondition; got: %v", err)
	}
}
