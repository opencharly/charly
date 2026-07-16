package main

import (
	"github.com/opencharly/sdk/spec"
	"testing"
)

// TestCollectDescriptions_BakesPluginFileCheck is the main-repo equivalent of the
// box/fedora "confirm a migrated plugin: file check baked into the ai.opencharly.description
// label" verify: a candy plan carrying a deterministic `check: { plugin: file }` step is
// collected into the LabelDescriptionSet (the payload of the ai.opencharly.description OCI
// label). Baking is verb-agnostic (bakeableSteps collects every check step regardless of
// verb), so the migrated plugin: file checks across the corpus bake exactly like any check.
func TestCollectDescriptions_BakesPluginFileCheck(t *testing.T) {
	layers := map[string]*Candy{
		"redis": {
			Name:        "redis",
			Description: "redis store",
			plan: []Step{
				{Check: "the redis binary exists", Op: Op{
					ID:          "redis-binary",
					Plugin:      "file",
					PluginInput: map[string]any{"file": "/usr/bin/redis-server", "exists": true},
				}},
			},
		},
	}
	cfg := &Config{Box: boxMapOf(map[string]spec.BoxConfig{
		"redis-box": {Candy: []string{"redis"}},
	})}

	set := CollectDescriptions(cfg, layers, "redis-box")
	if set == nil || len(set.Candy) != 1 {
		t.Fatalf("CollectDescriptions = %+v, want one candy description", set)
	}
	baked := set.Candy[0].Plan
	if len(baked) != 1 {
		t.Fatalf("baked plan = %+v, want the one plugin: file check", baked)
	}
	step := baked[0]
	if step.Plugin != "file" {
		t.Errorf("baked step verb = %q, want plugin: file", step.Plugin)
	}
	if step.PluginInput["file"] != "/usr/bin/redis-server" {
		t.Errorf("baked plugin_input.file = %v, want /usr/bin/redis-server", step.PluginInput)
	}
}

// TestBakeableSteps_StampsIntentDo is the regression guard for the K3-D+ baked-label side-channel:
// the ai.opencharly.description label must carry the keyword-derived intent_do on every
// verb-carrying baked step. Moving the validate ENGINE to candy/plugin-box killed the old side
// effect (validate mutating the shared structs the bake serialized), so bakeableSteps now stamps
// its own COPY. Without the stamp, baked[0].Op.IntentDo is empty and this test fails.
func TestBakeableSteps_StampsIntentDo(t *testing.T) {
	baked := bakeableSteps([]Step{
		// a plugin: file CHECK step (VerbsSet = ["plugin"]) → intent_do "assert"
		{Check: "the binary exists", Op: Op{Plugin: "file", PluginInput: map[string]any{"file": "/usr/bin/x", "exists": true}}},
		// a verb-less AGENT-CHECK step → no Op verb, IntentDo stays empty (matches the pre-cutover bake)
		{AgentCheck: "the dashboard looks populated"},
	})
	if len(baked) != 2 {
		t.Fatalf("bakeableSteps = %d steps, want 2 (check + agent-check both bake)", len(baked))
	}
	if got := baked[0].IntentDo; got != string(DoAssert) {
		t.Errorf("check-step intent_do = %q, want %q (deterministic keyword stamp)", got, DoAssert)
	}
	if got := baked[1].IntentDo; got != "" {
		t.Errorf("verb-less agent-check intent_do = %q, want empty", got)
	}
}
