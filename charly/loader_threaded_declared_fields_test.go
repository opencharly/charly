package main

import (
	"testing"
)

// loader_threaded_declared_fields_test.go — the parse-guard FEED contract (Cutover C task 0,
// finding #1): loaderThreaded() must populate spec.Threaded.StructuralDeclaredFields from each
// structural kind's REGISTERED input schema (the process-wide compiled plugin schema set), never
// a hand-maintained word list. The sdk/loaderkit in-body member scan consults it to keep a
// structural body's DECLARED fields as data even when a field name collides with a kind word —
// a declared `iterate:` block carrying the kind word `agent:` (the corpus collision class,
// RCA'd live). A kind word absent from the map falls back to every kind-word key being a
// member — the documented no-declared-schema fallback — so an UNPOPULATED map silently
// re-opens the collision class: this test pins the feed, not just the fallback.
//
// Since the group-kind removal (Cutover C task 1) NO BUILTIN structural kind remains
// registered with an input schema (group was the last) — so the unit-level pins here are
// the REMOVAL side (the dead kind's schema must not leak back through a stale
// registration) and the registry-fed provenance of every surviving entry; the POSITIVE
// feed is pinned at the integration level by TestExternalStructKind_StructuralDecode
// (plugin_structkind_e2e_test.go), whose external structural plugin's input schema IS
// registered by the prescan.
func TestLoaderThreaded_StructuralDeclaredFields(t *testing.T) {
	threaded := loaderThreaded()
	if _, ok := threaded.StructuralDeclaredFields["group"]; ok {
		t.Fatal("the removed group kind must not appear in StructuralDeclaredFields — a stale compiled-in registration is still feeding its input schema (the residual group: node would load instead of hard-erroring)")
	}
	pluginSchemas.mu.Lock()
	defer pluginSchemas.mu.Unlock()
	for kind := range threaded.StructuralDeclaredFields {
		if _, ok := pluginSchemas.inputDefs[provKey(ClassKind, kind)]; !ok {
			t.Errorf("StructuralDeclaredFields[%q] has no registered input def — the map must be fed from the registry (R3), never a hand-maintained list", kind)
		}
	}
	for kind := range threaded.DeployDeclaredFields {
		if _, ok := pluginSchemas.inputDefs[provKey(ClassKind, kind)]; !ok {
			t.Errorf("DeployDeclaredFields[%q] has no registered input def — the map must be fed from the registry (R3), never a hand-maintained list", kind)
		}
	}
}

// TestLoaderThreaded_DeployDeclaredFields: the SUBSTRATE feed (Cutover C task 1, the
// sdk #225/#221 channel) populates the #Deploy-family words from their REGISTERED input
// schemas — pod must declare the body fields whose VALUE SHAPES would otherwise
// misclassify as in-substrate members (iterate carrying the `agent:` kind-word key is
// the canonical ADE-iterate-bed regression), and must never declare an authored member
// name.
func TestLoaderThreaded_DeployDeclaredFields(t *testing.T) {
	// The feed is REGISTRY-driven, so the test registers a synthetic substrate word
	// through the SAME mechanism a real plugin uses (registerPluginUnitSchema + the
	// prescan's declaredDeploySubstrate) — a hand-maintained word list is exactly what
	// the feed exists to prevent. The synthetic def carries the #Deploy-family field
	// names whose VALUE SHAPES would otherwise misclassify as in-substrate members
	// (iterate carrying the `agent:` kind-word key is the canonical ADE-iterate-bed
	// regression, sdk #221).
	prior := declaredDeploySubstrate["testsubstrate"]
	declaredDeploySubstrate["testsubstrate"] = true
	t.Cleanup(func() {
		if prior {
			declaredDeploySubstrate["testsubstrate"] = true
		} else {
			delete(declaredDeploySubstrate, "testsubstrate")
		}
		pluginSchemas.mu.Lock()
		delete(pluginSchemas.inputDefs, provKey(ClassKind, "testsubstrate"))
		pluginSchemas.mu.Unlock()
	})
	if err := registerPluginUnitSchema("testsubstrate", PluginSchema{
		CueSource: "#TestsubstrateInput: {from?: string, image?: string, disposable?: bool, lifecycle?: string, description?: string, plan?: _, iterate?: _, record?: _, instrument?: _}\n",
		InputDefs: map[string]string{provKey(ClassKind, "testsubstrate"): "#TestsubstrateInput"},
	}); err != nil {
		t.Fatalf("registerPluginUnitSchema: %v", err)
	}
	threaded := loaderThreaded()
	fields, ok := threaded.DeployDeclaredFields["testsubstrate"]
	if !ok {
		t.Fatal("the registered testsubstrate input schema was not fed into DeployDeclaredFields — the substrate parse guard's declared-field channel is inert")
	}
	for _, want := range []string{"from", "image", "disposable", "lifecycle", "description", "plan", "iterate", "record", "instrument"} {
		if !fields[want] {
			t.Errorf("testsubstrate declared fields missing %q", want)
		}
	}
	if fields["web"] || fields["cache"] {
		t.Error("declared fields must be SCHEMA fields only — authored member names must never leak into the declared set")
	}
}
