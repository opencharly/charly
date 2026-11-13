package main

// host_build_construct_step_test.go — the STEP-INPUT half of opencharly/charly#1131: a class:step
// whose declared contract opts in with plugin_paths receives the CONNECTED PLUGIN SET in its
// input, and a step that does not opt in receives its AUTHORED input untouched. The second arm is
// the boundary: a step's input is validated against its own (usually closed) schema, so injecting
// a key a step did not declare would be a validation failure, not a gift.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/opencharly/spec/spec"
)

// connectedStepFake is a class:step provider carrying a DECLARED contract — the same shape
// capMeta gives a real class:step provider, without building an out-of-process plugin binary.
type connectedStepFake struct {
	word     string
	contract spec.StepContract
}

func (f connectedStepFake) Reserved() string     { return f.word }
func (f connectedStepFake) Class() ProviderClass { return ClassStep }
func (f connectedStepFake) Invoke(context.Context, *Operation) (*Result, error) {
	return nil, nil
}
func (f connectedStepFake) DeclaredStepContract() (spec.StepContract, bool) {
	return f.contract, true
}

var _ spec.StepContractCarrier = connectedStepFake{}

// constructedPayload runs the construct-step host builder for one class:step word and returns the
// external step's decoded input.
func constructedPayload(t *testing.T, word string, input map[string]any) map[string]any {
	t.Helper()
	reply, err := hostBuildConstructStep(context.Background(), spec.ConstructStepRequest{
		Op:        spec.Op{Plugin: word, PluginInput: input},
		CandyName: "connected-step-candy",
	}, buildEngineContext{})
	if err != nil {
		t.Fatalf("hostBuildConstructStep(%q): %v", word, err)
	}
	if reply.Step == nil {
		t.Fatalf("hostBuildConstructStep(%q) constructed no step", word)
	}
	step, err := spec.StepFromView(*reply.Step)
	if err != nil {
		t.Fatalf("re-materialize the constructed step: %v", err)
	}
	ext, ok := step.(*spec.ExternalStep)
	if !ok {
		t.Fatalf("constructed step is %T, want *spec.ExternalStep (a declared class:step kind)", step)
	}
	var fields map[string]any
	if err := json.Unmarshal(ext.Payload, &fields); err != nil {
		t.Fatalf("decode the step input %s: %v", ext.Payload, err)
	}
	return fields
}

func TestConstructStepInjectsTheConnectedPluginSetForAnOptedInStep(t *testing.T) {
	isolateProviderRegistry(t)
	connectedPluginSet.forget()
	t.Cleanup(connectedPluginSet.forget)

	fake := connectedStepFake{word: "plugin-tree-fake", contract: spec.StepContract{
		Scope: spec.ScopeSystem, Venue: spec.VenueHostNative, Gate: spec.GateNone, Emits: false,
		PluginPaths: true,
	}}
	if err := providerRegistry.register(fake, originBuiltin); err != nil {
		t.Fatalf("register the fake class:step provider: %v", err)
	}
	recordConnectedPluginFromLoad(
		"plugin-firecracker",
		"/cache/plugin-build/views/plugin-firecracker-0123456789abcdef",
		"/src/plugin-firecracker",
	)

	fields := constructedPayload(t, "plugin-tree-fake", map[string]any{
		"plugins": []any{"plugin-firecracker"},
	})

	// The AUTHORED input stays exactly as authored: `plugins: [<leaf>]` is the plan's spelling.
	authored, ok := fields["plugins"].([]any)
	if !ok || len(authored) != 1 || authored[0] != "plugin-firecracker" {
		t.Fatalf("the authored plugins list was not carried verbatim: %v", fields["plugins"])
	}

	raw, ok := fields[spec.ConnectedPluginsInputKey]
	if !ok {
		t.Fatalf("an opted-in step's input carries no %q key: %v", spec.ConnectedPluginsInputKey, fields)
	}
	entries, ok := raw.([]any)
	if !ok || len(entries) != 1 {
		t.Fatalf("%q = %v, want one connected-plugin entry", spec.ConnectedPluginsInputKey, raw)
	}
	entry, _ := entries[0].(map[string]any)
	if entry["leaf"] != "plugin-firecracker" {
		t.Errorf("entry leaf = %v, want the served plugin's leaf", entry["leaf"])
	}
	if entry["view_path"] != "/cache/plugin-build/views/plugin-firecracker-0123456789abcdef" {
		t.Errorf("entry view_path = %v, want the binary the connect served", entry["view_path"])
	}
	if entry["src_dir"] != "/src/plugin-firecracker" {
		t.Errorf("entry src_dir = %v, want the candy source dir", entry["src_dir"])
	}
}

func TestConstructStepLeavesANonOptedInStepInputUntouched(t *testing.T) {
	isolateProviderRegistry(t)
	connectedPluginSet.forget()
	t.Cleanup(connectedPluginSet.forget)

	fake := connectedStepFake{word: "closed-input-fake", contract: spec.StepContract{
		Scope: spec.ScopeSystem, Venue: spec.VenueHostNative, Gate: spec.GateNone, Emits: false,
	}}
	if err := providerRegistry.register(fake, originBuiltin); err != nil {
		t.Fatalf("register the fake class:step provider: %v", err)
	}
	recordConnectedPluginFromLoad("plugin-firecracker", "/cache/views/plugin-firecracker-abc", "")

	fields := constructedPayload(t, "closed-input-fake", map[string]any{"marker": "hello"})
	if _, injected := fields[spec.ConnectedPluginsInputKey]; injected {
		t.Fatalf("a step that did not opt in received %q — its closed input schema would reject it: %v",
			spec.ConnectedPluginsInputKey, fields)
	}
	if fields["marker"] != "hello" {
		t.Fatalf("the authored input changed: %v", fields)
	}
}
