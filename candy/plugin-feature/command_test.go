package feature

import (
	"encoding/json"
	"testing"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/spec/spec"
)

// TestPlanSteps_FlattensKeywordAgentCheck proves planSteps (the transform K3 moved here from
// charly/host_build_feature.go) flattens a raw plan into the keyword/text/agent/check summary the
// list/pending output needs — the same shape the former host-side FeatureStep loop produced.
func TestPlanSteps_FlattensKeywordAgentCheck(t *testing.T) {
	plan := []spec.Step{
		{Check: "the true command runs", Op: spec.Op{Plugin: "command"}},
		{AgentCheck: "the service behaves"},
	}
	steps := planSteps(plan)
	if len(steps) != 2 {
		t.Fatalf("planSteps len = %d, want 2", len(steps))
	}
	if !steps[0].IsCheck || steps[0].IsAgent {
		t.Errorf("step 0 (check:) = %+v, want IsCheck=true IsAgent=false", steps[0])
	}
	if !steps[1].IsCheck || !steps[1].IsAgent {
		t.Errorf("step 1 (agent-check:) = %+v, want IsCheck=true IsAgent=true", steps[1])
	}
	if steps[0].Index != 0 || steps[1].Index != 1 {
		t.Errorf("indexes = %d,%d, want 0,1", steps[0].Index, steps[1].Index)
	}
}

// TestSummary_EmptyFallsBackToPlaceholder proves summary renders a description's info line, or the
// "(empty)" placeholder when the description carries no renderable info line.
func TestSummary_EmptyFallsBackToPlaceholder(t *testing.T) {
	if got := summary(""); got != "(empty)" {
		t.Errorf("summary(\"\") = %q, want \"(empty)\"", got)
	}
	if got := summary("A real one-line description"); got != "A real one-line description" {
		t.Errorf("summary(desc) = %q, want the description echoed back", got)
	}
}

// fakeReader wraps a spec.CandyModel + spec.CandyView into the spec.CandyReader the flatten walks
// (deploykit.NewSpecCandyModel is the SAME adapter loaderkit.FinalizeScannedCandies returns).
func fakeReader(name, desc string, plan []spec.Step) spec.CandyReader {
	return deploykit.NewSpecCandyModel(spec.CandyModel{Name: name, Plan: plan}, spec.CandyView{Description: desc})
}

// boxConfig marshals a BoxConfig into the opaque BoxMap entry flattenFeatures reads.
func boxConfig(name, desc string, plan []spec.Step) spec.BoxMap {
	raw, err := json.Marshal(spec.BoxConfig{Description: desc, Plan: plan})
	if err != nil {
		panic(err)
	}
	return spec.BoxMap{name: raw}
}

// TestFlattenFeatures_RawEntities is the RELOCATED coverage for the former
// charly/host_build_feature.go enumerateFeatures flatten loop (K-wave 2 cone R6 — the "feature"
// HostBuild seam is DELETED; the enumeration now lives in this plugin). It proves the flatten
// yields the SAME raw spec.FeatureEntity data the host seam produced: candy readers surface
// description + plan untransformed, content-less box images are omitted, and the filter narrows by
// kind or entity id.
func TestFlattenFeatures_RawEntities(t *testing.T) {
	plan := []spec.Step{{Check: "the true command runs", Op: spec.Op{Plugin: "command"}}}
	cfg := &spec.Config{Box: boxConfig("with-plan", "A content box", plan)}
	// A content-less box image must be OMITTED (matching the former engine).
	cfg.Box["empty-box"] = boxConfig("empty-box", "", nil)["empty-box"]

	layers := map[string]spec.CandyReader{
		"with-desc": fakeReader("with-desc", "A candy with a plan", plan),
		"bare":      fakeReader("bare", "", nil),
	}

	ents := flattenFeatures(cfg, layers, "")
	if len(ents) != 3 {
		t.Fatalf("flattenFeatures len = %d, want 3 (2 candies + 1 content box): %+v", len(ents), ents)
	}
	byID := map[string]spec.FeatureEntity{}
	for _, e := range ents {
		byID[e.Kind+":"+e.Name] = e
	}
	if e, ok := byID["candy:with-desc"]; !ok || e.Description != "A candy with a plan" || len(e.Plan) != 1 || e.Plan[0].Check != "the true command runs" {
		t.Fatalf("candy:with-desc enumerated wrong: %+v", byID["candy:with-desc"])
	}
	if e, ok := byID["candy:bare"]; !ok || e.Description != "" || len(e.Plan) != 0 {
		t.Fatalf("candy:bare enumerated wrong: %+v", byID["candy:bare"])
	}
	if e, ok := byID["box:with-plan"]; !ok || e.Description != "A content box" || len(e.Plan) != 1 {
		t.Fatalf("box:with-plan enumerated wrong: %+v", byID["box:with-plan"])
	}
	if _, ok := byID["box:empty-box"]; ok {
		t.Fatalf("content-less box must be omitted, got %+v", byID["box:empty-box"])
	}
}

// TestFlattenFeatures_Filter proves the filter narrows by kind ("candy"/"box") or by full entity
// id ("candy:name"), matching the former host seam's eid-vs-kind match.
func TestFlattenFeatures_Filter(t *testing.T) {
	cfg := &spec.Config{Box: boxConfig("b", "a box", nil)}
	layers := map[string]spec.CandyReader{"c": fakeReader("c", "a candy", nil)}

	if got := flattenFeatures(cfg, layers, "candy"); len(got) != 1 || got[0].Kind != "candy" || got[0].Name != "c" {
		t.Fatalf("filter candy = %+v, want exactly candy:c", got)
	}
	if got := flattenFeatures(cfg, layers, "box"); len(got) != 1 || got[0].Kind != "box" || got[0].Name != "b" {
		t.Fatalf("filter box = %+v, want exactly box:b", got)
	}
	if got := flattenFeatures(cfg, layers, "candy:c"); len(got) != 1 || got[0].Kind != "candy" || got[0].Name != "c" {
		t.Fatalf("filter candy:c = %+v, want exactly candy:c", got)
	}
	if got := flattenFeatures(cfg, layers, "candy:missing"); len(got) != 0 {
		t.Fatalf("filter candy:missing = %+v, want zero", got)
	}
}
