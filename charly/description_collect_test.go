package main

import "testing"

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
	cfg := &Config{Box: boxMapOf(map[string]BoxConfig{
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
