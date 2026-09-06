package main

import (
	"context"
	"sync"
	"testing"

	"cuelang.org/go/cue"
	"github.com/opencharly/spec/spec"
)

// TestRunPluginVerb_GatesBuiltinSchemaBeforeAuthoredInputValidation reproduces and
// gates the box-mode schema-registration ORDERING gap: a COMPILED-IN plugin unit
// registers its PROVIDERS at init() (RegisterBuiltinPluginUnit) but its SERVED SCHEMA
// only through the sync.Once loadBuiltinPluginUnits gate. A box-mode check run inside
// a fresh container (charly check live on a deployed bed) reaches runPluginVerb
// through an entry path that runs NO project plugin load, so connectBakedPlugin
// resolves the init-registered builtin provider IMMEDIATELY (registry hit) and
// validation then ran against an EMPTY input-def registry — every `plugin verb:*`
// check step failed with "no input def registered (schema not loaded)" while the
// deploy phase (whose loadDeployPlugins path runs the gate, plugin_loader.go)
// passed. The kind-class dispatch already gates before validating
// (provider_kind_invoke.go runPluginKind); this pins the SAME ordering on the verb
// path.
//
// The test simulates the production state by clearing the process-wide plugin
// schema set (as if no gate had run) and re-arming the once-gate TestMain spent
// (which exists precisely because this state is otherwise unobservable in-process
// — see test_main_test.go). Before the fix this test FAILS with the production
// message; after it, the in-dispatch gate re-registers the builtin schemas and the
// verb passes.
func TestRunPluginVerb_GatesBuiltinSchemaBeforeAuthoredInputValidation(t *testing.T) {
	// Snapshot + clear the process-wide schema set: the "fresh container" state.
	pluginSchemas.mu.Lock()
	savedSources := pluginSchemas.sources
	savedDefs := pluginSchemas.inputDefs
	savedUnified := pluginSchemas.unified
	pluginSchemas.sources = nil
	pluginSchemas.inputDefs = map[string]string{}
	pluginSchemas.unified = cue.Value{}
	pluginSchemas.mu.Unlock()

	// Reset the once-gate so the dispatch's own load can re-run (TestMain spent it).
	builtinGateOnce = sync.Once{}
	builtinGateErr = nil
	defer func() {
		// Restore the exact pre-test process-wide state; the once-gate stays spent
		// (re-armed to a fresh Once that the next dispatch spends), which matches
		// the TestMain-completed state the rest of the suite expects.
		pluginSchemas.mu.Lock()
		pluginSchemas.sources = savedSources
		pluginSchemas.inputDefs = savedDefs
		pluginSchemas.unified = savedUnified
		pluginSchemas.mu.Unlock()
		builtinGateOnce = sync.Once{}
		builtinGateErr = nil
	}()

	// The ordering contract: the dispatch itself must register the builtin schemas
	// before validating — even from the cleared state.
	r := hostVerbResolverFor(nil, spec.CheckModeBox)
	op := &spec.Op{Plugin: "exampleprobe", PluginInput: map[string]any{"marker": "gate-marker"}}
	res := r.runPluginVerb(context.Background(), op)
	if res.Status != spec.StatusPass {
		t.Fatalf("plugin verb after schema-gate clearing: status=%v msg=%q, want pass — the verb dispatch must register the compiled-in plugin schemas BEFORE authored-input validation (production repro: box-mode check steps failing with 'no input def registered (schema not loaded)')", res.Status, res.Message)
	}
	if res.Message != "gate-marker" {
		t.Fatalf("exampleprobe message=%q, want gate-marker (plugin_input round-trip intact)", res.Message)
	}
}
