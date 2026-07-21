package main

import (
	"context"
	"testing"

	"github.com/opencharly/sdk/spec"
)

// TestRunPluginVerb_Dispatch proves the generic `plugin:` verb dispatches through
// the provider registry to the built-in exampleprobe provider, that plugin_input
// round-trips author → provider → result, and that an unregistered plugin verb
// FAILS (not skips — a bed must go red, not fake-green).
func TestRunPluginVerb_Dispatch(t *testing.T) {
	r := hostVerbResolverFor(nil, RunModeBox)

	op := &spec.Op{Plugin: "exampleprobe", PluginInput: map[string]any{"marker": "unit-marker"}}
	res := r.runPluginVerb(context.Background(), op)
	if res.Status != spec.StatusPass {
		t.Fatalf("exampleprobe status=%v msg=%q, want pass", res.Status, res.Message)
	}
	if res.Message != "unit-marker" {
		t.Fatalf("exampleprobe message=%q, want unit-marker (plugin_input round-trip)", res.Message)
	}

	miss := r.runPluginVerb(context.Background(), &spec.Op{Plugin: "nonexistent-verb"})
	if miss.Status != spec.StatusFail {
		t.Fatalf("unregistered plugin verb status=%v, want fail", miss.Status)
	}
}

// TestValidatePluginCandy proves the builtin-provider assertion: a candy
// declaring a registered builtin verb validates; one naming an unregistered
// builtin or a malformed capability fails.
func TestValidatePluginCandy(t *testing.T) {
	ok := &spec.Plugin{Source: "builtin", Providers: []spec.PluginCapability{"verb:exampleprobe"}}
	if issues := validatePluginCandy("ex", ok); len(issues) != 0 {
		t.Fatalf("registered builtin should validate, got %v", issues)
	}
	bad := &spec.Plugin{Source: "builtin", Providers: []spec.PluginCapability{"verb:nonexistent"}}
	if len(validatePluginCandy("bad", bad)) == 0 {
		t.Fatalf("unregistered builtin provider should fail validation")
	}
	mal := &spec.Plugin{Source: "builtin", Providers: []spec.PluginCapability{"notacapability"}}
	if len(validatePluginCandy("mal", mal)) == 0 {
		t.Fatalf("malformed capability should fail validation")
	}
}
