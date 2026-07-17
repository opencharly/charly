package feature

import (
	"testing"

	"github.com/opencharly/sdk/spec"
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
