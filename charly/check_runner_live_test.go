package main

import (
	"context"
	"github.com/opencharly/sdk/spec"
	"strings"
	"testing"
)

// TestRunCheckLive_PureCycleEmitsFailVerdictsNoPropagation exercises the
// depends_on cycle handling: with every scored step in a cycle, topoSortScored
// returns an empty ordered set + the cyclic remainder, and RunCheckLive emits
// one fail verdict per cyclic step (SkippedReason prefix "cycle:") instead of
// erroring out. Plan-unify re-keys this from scenario- to step-level.
func TestRunCheckLive_PureCycleEmitsFailVerdictsNoPropagation(t *testing.T) {
	// a depends_on b, b depends_on a — pure cycle (id-keyed).
	// venue is loader-derived (yaml:"-") from tree position; this in-package test
	// sets it directly to stand in for the flatten pass.
	plan := []spec.Step{
		{Check: "a", Op: Op{ID: "a", Venue: "test-pod", DependsOn: []string{"b"}, Plugin: "file", PluginInput: map[string]any{"file": "/a"}}},
		{Check: "b", Op: Op{ID: "b", Venue: "test-pod", DependsOn: []string{"a"}, Plugin: "file", PluginInput: map[string]any{"file": "/b"}}},
	}
	res, err := RunCheckLive(context.Background(), "", "test-score", plan)
	if err != nil {
		t.Fatalf("a depends_on cycle must NOT propagate as an error — got: %v", err)
	}
	if res == nil {
		t.Fatalf("expected non-nil CheckRunResults even on a pure cycle")
	}
	if len(res.Step) != 2 {
		t.Fatalf("2 cyclic steps → 2 fail verdicts, got %d", len(res.Step))
	}
	for _, sc := range res.Step {
		if sc.Status != "fail" {
			t.Errorf("step %q: status = %q, want fail", sc.ID, sc.Status)
		}
		if !strings.HasPrefix(sc.SkippedReason, "cycle:") {
			t.Errorf("step %q: SkippedReason = %q, want prefix 'cycle:'", sc.ID, sc.SkippedReason)
		}
	}
	if res.Summary.Fail != 2 || res.Summary.Total != 2 {
		t.Errorf("summary mismatch: total=%d fail=%d, want total=2 fail=2", res.Summary.Total, res.Summary.Fail)
	}
}

// TestRunCheckLive_EmptyInputReturnsEarly is a regression on the empty-plan
// fast path.
func TestRunCheckLive_EmptyInputReturnsEarly(t *testing.T) {
	res, err := RunCheckLive(context.Background(), "", "test-score", nil)
	if err != nil {
		t.Fatalf("nil plan should not error: %v", err)
	}
	if res == nil || len(res.Step) != 0 {
		t.Errorf("nil plan should yield empty result; got %v", res)
	}
}
