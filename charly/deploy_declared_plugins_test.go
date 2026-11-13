package main

// deploy_declared_plugins_test.go — the DEPLOY connect's declared-candy widening
// (opencharly/charly#1131): the plugin candies a deployment declares are part of the work at
// hand, even when no plan step references their words, because a deploy's venue also dispatches
// through a SHELL that no prescan can read.
//
// Both units are pure, so they are asserted without a project scan:
//   - templateCandyRefs follows a `from:` chain into the local templates and survives a cycle;
//   - addDeclaredDeployPluginWords adds ONLY declared, out-of-tree PLUGIN candies — a plain candy
//     gains no build, a compiled-in plugin gains none, and a plugin nothing declared stays out.

import (
	"encoding/json"
	"testing"

	"github.com/opencharly/spec/spec"
)

// declaredPluginFake answers only the three accessors the widening reads; the embedded nil
// interface makes any OTHER call panic, which is the loud failure an unexpected dependency owes.
type declaredPluginFake struct {
	spec.CandyReader
	plugin bool
	source string
	words  []string
}

func (f declaredPluginFake) IsPluginCandy() bool          { return f.plugin }
func (f declaredPluginFake) GetPluginSource() string      { return f.source }
func (f declaredPluginFake) GetPluginProviders() []string { return f.words }

// The plan-scoping half reads these two on EVERY scanned candy, so a fake used across both
// halves must answer them: no external builder, no plan steps.
func (f declaredPluginFake) GetExternalBuilder() string { return "" }
func (f declaredPluginFake) PlanSteps() []spec.Step     { return nil }

func TestAddDeclaredDeployPluginWordsAddsOnlyDeclaredOutOfTreePluginCandies(t *testing.T) {
	candyMap := map[string]spec.CandyReader{
		"github.com/org/plugin-a/candy/plugin-a": declaredPluginFake{
			plugin: true, source: "github.com/org/plugin-a/candy/plugin-a",
			words: []string{"verb:alpha", "command:beta"},
		},
		"plugin-local":   declaredPluginFake{plugin: true, source: "github.com/org/plugin-local/candy/plugin-local", words: []string{"step:gamma"}},
		"plain-candy":    declaredPluginFake{plugin: false},
		"builtin-plugin": declaredPluginFake{plugin: true, source: "builtin", words: []string{"verb:builtin"}},
		// Present in the scan, but NOTHING the deploy declares: it must stay unconnected.
		"plugin-undeclared": declaredPluginFake{plugin: true, source: "github.com/org/undeclared/candy/plugin-undeclared", words: []string{"verb:hidden"}},
	}
	refs := map[string]struct{}{}
	addDeclaredDeployPluginWords(candyMap, []spec.ExtraCandyRef{
		{Ref: "@github.com/org/plugin-a/candy/plugin-a:v1"},
		{Ref: "plugin-local"},
		{Ref: "plain-candy"},
		{Ref: "builtin-plugin"},
	}, refs)

	for _, want := range []string{"alpha", "beta", "gamma"} {
		if _, ok := refs[want]; !ok {
			t.Errorf("word %q missing: the deployment DECLARED the plugin that serves it", want)
		}
	}
	for _, unwanted := range []string{"hidden", "builtin"} {
		if _, ok := refs[unwanted]; ok {
			t.Errorf("word %q was connected: %q is not a declared out-of-tree plugin candy", unwanted, unwanted)
		}
	}
	if len(refs) != 3 {
		t.Fatalf("refs = %v, want exactly the three declared words", refs)
	}
}

// TestDeployConnectRefsJoinsBothHalves is the coverage the WIRING needs: the two halves are
// useless apart, so this asserts the JOINED set. Deleting either half from deployConnectRefs turns
// exactly one of the two arms red — a test on either half alone would stay green.
func TestDeployConnectRefsJoinsBothHalves(t *testing.T) {
	candyMap := map[string]spec.CandyReader{
		"planned-plugin":  declaredPluginFake{plugin: true, source: "github.com/org/planned/candy/planned-plugin", words: []string{"verb:planned"}},
		"declared-plugin": declaredPluginFake{plugin: true, source: "github.com/org/declared/candy/declared-plugin", words: []string{"command:venuecli"}},
	}
	// refWords is what the PLAN references; the declared list is what the deploy CARRIES.
	refs := deployConnectRefs(&spec.Config{}, candyMap, []string{"planned"}, []spec.ExtraCandyRef{{Ref: "declared-plugin"}})

	for _, want := range []string{"planned", "venuecli"} {
		if _, ok := refs[want]; !ok {
			t.Errorf("word %q missing from the deploy connect's reference set", want)
		}
	}
	if len(refs) != 2 {
		t.Fatalf("refs = %v, want exactly the plan word plus the declared plugin's word", refs)
	}
}

func TestTemplateCandyRefsFollowsTheFromChainAndSurvivesACycle(t *testing.T) {
	cfg := &spec.Config{Local: map[string]json.RawMessage{
		"leaf-template": json.RawMessage(`{"candy":["plugin-deploy-firecracker","@github.com/org/plugin-firecracker/candy/plugin-firecracker:v1"]}`),
		"mid-template":  json.RawMessage(`{"candy":["mid-candy"],"from":"leaf-template"}`),
		"loop-a":        json.RawMessage(`{"candy":["a-candy"],"from":"loop-b"}`),
		"loop-b":        json.RawMessage(`{"candy":["b-candy"],"from":"loop-a"}`),
	}}

	got := templateCandyRefs(cfg, "mid-template")
	want := []string{"mid-candy", "plugin-deploy-firecracker", "@github.com/org/plugin-firecracker/candy/plugin-firecracker:v1"}
	if len(got) != len(want) {
		t.Fatalf("templateCandyRefs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("templateCandyRefs = %v, want %v", got, want)
		}
	}

	// A cycle terminates instead of looping, and each member's candies are still reported once.
	loop := templateCandyRefs(cfg, "loop-a")
	if len(loop) != 2 {
		t.Fatalf("a cyclic from: chain returned %v, want both candies once", loop)
	}
	// An unknown / absent template yields nothing rather than an error: a vm/pod `from:` is not a
	// candy list.
	if refs := templateCandyRefs(cfg, "not-a-local-template"); len(refs) != 0 {
		t.Fatalf("an unknown from: returned %v, want nothing", refs)
	}
	if refs := templateCandyRefs(nil, "x"); len(refs) != 0 {
		t.Fatalf("a nil config returned %v, want nothing", refs)
	}
}
