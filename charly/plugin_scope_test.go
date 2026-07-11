package main

import (
	"testing"

	"github.com/opencharly/sdk/spec"
)

// TestCollectReferencedPluginWords_Scoping gates the loadProjectPlugins perf-scoping:
// the reference collector + selection predicate load ONLY the plugin candies the work
// at hand references, NEVER under-loading a referenced one. It mirrors the real beds —
// a verb referenced in a candy run-step (check-pod's examplestep), a builder selected
// via external_builder (check-pod's examplebuilder), a verb referenced inline in a bed
// plan whose plugin was add_candy'd (check-local / a `spice:` step), a box-plan check
// verb, and an external deploy substrate (check-exampledeploy) — against an UNREFERENCED
// plugin candy that must be skipped.
func TestCollectReferencedPluginWords_Scoping(t *testing.T) {
	// Four external plugin candies. examplestep/examplebuilder/spice/exampledeploy are
	// each referenced by some site below; unusedverb is referenced NOWHERE.
	stepPlugin := &CandyPluginDecl{Source: "github.com/x/step", Providers: []spec.PluginCapability{"verb:examplestep"}}
	builderPlugin := &CandyPluginDecl{Source: "github.com/x/builder", Providers: []spec.PluginCapability{"builder:examplebuilder"}}
	addedPlugin := &CandyPluginDecl{Source: "github.com/x/spice", Providers: []spec.PluginCapability{"verb:spice"}}
	deployPlugin := &CandyPluginDecl{Source: "github.com/x/deploy", Providers: []spec.PluginCapability{"deploy:exampledeploy"}}
	boxVerbPlugin := &CandyPluginDecl{Source: "github.com/x/boxverb", Providers: []spec.PluginCapability{"verb:boxverb"}}
	unrefdPlugin := &CandyPluginDecl{Source: "github.com/x/unused", Providers: []spec.PluginCapability{"verb:unusedverb"}}

	candies := map[string]*Candy{
		// A consumer candy whose run-step references the examplestep verb (the build-emit leg).
		"examplestep-consumer": {Name: "examplestep-consumer", plan: []Step{
			{Run: "bake at build", Op: Op{Plugin: "examplestep"}},
		}},
		// A consumer candy that selects an external builder via external_builder.
		"examplebuilder-consumer": {Name: "examplebuilder-consumer", ExternalBuilder: "examplebuilder"},
		// The plugin candies themselves (their own plans reference nothing).
		"plugin-example-step":    {Name: "plugin-example-step", Plugin: stepPlugin},
		"plugin-example-builder": {Name: "plugin-example-builder", Plugin: builderPlugin},
		"plugin-spice":           {Name: "plugin-spice", Plugin: addedPlugin},
		"plugin-example-deploy":  {Name: "plugin-example-deploy", Plugin: deployPlugin},
		"plugin-boxverb":         {Name: "plugin-boxverb", Plugin: boxVerbPlugin},
		"plugin-unused":          {Name: "plugin-unused", Plugin: unrefdPlugin},
		"ordinary":               {Name: "ordinary"}, // no plugin, no plan
	}
	// A box whose baked plan authors a plugin check verb directly (boxverb) — the
	// box-plan reference site (a baked plan runs at check live).
	boxes := boxMapOf(map[string]BoxConfig{
		"some-box": {Plan: []Step{{Check: "probe via boxverb", Op: Op{Plugin: "boxverb"}}}},
	})
	// The deploy node's OWN references (deployNodePluginContext output): the inline
	// `spice` bed-plan verb (its plugin came via add_candy) + the external deploy
	// substrate word `exampledeploy`.
	extra := []string{"spice", "exampledeploy"}

	refs := collectReferencedPluginWords(candies, boxes, extra)

	// Every referenced word is present.
	for _, w := range []string{"examplestep", "examplebuilder", "spice", "exampledeploy", "boxverb"} {
		if _, ok := refs[w]; !ok {
			t.Fatalf("referenced word %q missing from the collected set (under-load risk)", w)
		}
	}
	// The unreferenced word is absent (so its plugin is skipped — the perf win).
	if _, ok := refs["unusedverb"]; ok {
		t.Fatalf("word %q is referenced nowhere but was collected (over-scoped)", "unusedverb")
	}

	// Selection: every referenced plugin (verb / external_builder / add_candy-inline /
	// deploy-substrate / box-plan) is selected; the unreferenced one is NOT.
	selected := map[string]*CandyPluginDecl{
		"examplestep (run-step verb)":       stepPlugin,
		"examplebuilder (external_builder)": builderPlugin,
		"spice (add_candy inline bed verb)": addedPlugin,
		"exampledeploy (deploy substrate)":  deployPlugin,
		"boxverb (box-plan check verb)":     boxVerbPlugin,
	}
	for label, p := range selected {
		if !pluginProvidesReferencedWord(p, refs) {
			t.Fatalf("plugin %s must be selected (no under-load)", label)
		}
	}
	if pluginProvidesReferencedWord(unrefdPlugin, refs) {
		t.Fatalf("the unreferenced plugin must NOT be selected (it would waste a host build/connect)")
	}
}

// TestCollectReferencedPluginWords_ClassAgnostic proves the selection matches on the
// WORD alone, independent of class: a word collected from one site selects a plugin
// providing that word in ANY class. This is the over-load-safe property — a class
// mismatch can never UNDER-load (drop a referenced plugin), the HARD CONSTRAINT.
func TestCollectReferencedPluginWords_ClassAgnostic(t *testing.T) {
	// `shared` is referenced as a verb in a candy plan; a plugin provides it as a STEP.
	candies := map[string]*Candy{
		"consumer": {Name: "consumer", plan: []Step{{Run: "use shared", Op: Op{Plugin: "shared"}}}},
	}
	refs := collectReferencedPluginWords(candies, nil, nil)
	stepProvider := &CandyPluginDecl{Source: "github.com/x/s", Providers: []spec.PluginCapability{"step:shared"}}
	if !pluginProvidesReferencedWord(stepProvider, refs) {
		t.Fatalf("a word referenced in one class must select a plugin providing it in another (class-agnostic, no under-load)")
	}
	// A malformed capability is skipped, not a match.
	malformed := &CandyPluginDecl{Source: "github.com/x/m", Providers: []spec.PluginCapability{"shared", "noclass:"}}
	if pluginProvidesReferencedWord(malformed, refs) {
		t.Fatalf("a malformed capability string must not match")
	}
}
