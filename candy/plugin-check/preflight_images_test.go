package check

import (
	"reflect"
	"testing"

	"github.com/opencharly/spec/spec"
)

func TestPreflightImageCandidates_EmptyPlan(t *testing.T) {
	if got := preflightImageCandidates(nil); got != nil {
		t.Errorf("expected nil for empty plan, got %v", got)
	}
}

func TestPreflightImageCandidates_DedupSortSkipDotted(t *testing.T) {
	plan := []spec.Step{
		{Op: spec.Op{Venue: "web"}},
		{Op: spec.Op{Venue: "chrome"}},
		{Op: spec.Op{Venue: "web"}}, // duplicate
		{Op: spec.Op{Venue: ""}},    // no venue — skipped
		{Op: spec.Op{Venue: "a.b"}}, // dotted (nested child) — skipped
	}
	got := preflightImageCandidates(plan)
	want := []string{"chrome", "web"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("preflightImageCandidates = %v, want %v", got, want)
	}
}
