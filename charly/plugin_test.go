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

// TestValidatePluginCandy DELETED (dead-code-radical-removal batch): its subject,
// validatePluginCandy, was core's per-candy `plugin:` block validator — REMOVED from
// the per-candy validation loop in c9befd83 (the K3-D+ engine move to candy/plugin-box)
// alongside every other hand-rolled rule in that loop. Confirmed a live, coverage-
// identical twin at candy/plugin-box/validate_rules.go (the `if v.IsPlugin { ... }`
// block, whose own comment states it "Mirrors core splitCapability + validatePluginCandy
// exactly") — same three checks: ≥1 provider declared, each capability well-formed
// <class>:<word>, every builtin provider actually compiled in. (Aside, not itself a
// regression: plugin-box's own test suite doesn't yet have a DEDICATED unit test for
// this specific rule — worth a follow-up test-coverage addition, tracked separately,
// since the rule itself is confirmed present and correct in the live validate_rules.go
// code path exercised by every `charly box validate` run.)
