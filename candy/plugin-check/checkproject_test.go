package check

import (
	"encoding/json"
	"testing"

	"github.com/opencharly/sdk/spec"
)

// TestExpandPlanIncludes_CandyPlan exercises the candy arm of the relocated include-splicer
// (formerly charly/plan_unify_test.go's TestPlanUnify_IncludeSplicesCandyPlan): an `include:
// candy:X` step splices the referenced candy model's plan in place, stamping the source origin.
func TestExpandPlanIncludes_CandyPlan(t *testing.T) {
	rp := &spec.ResolvedProject{
		CandyModels: map[string]spec.CandyModel{
			"redis": {Plan: []spec.Step{
				{Check: "redis answers ping"},
				{Check: "redis binary present"},
			}},
		},
	}
	out, err := expandPlanIncludes(rp, []spec.Step{{Include: "candy:redis"}})
	if err != nil {
		t.Fatalf("expandPlanIncludes(candy): %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("include should splice 2 steps, got %d: %+v", len(out), out)
	}
	if out[0].Check != "redis answers ping" || out[1].Check != "redis binary present" {
		t.Errorf("spliced steps not in order: %+v", out)
	}
	if out[0].Origin != "candy:redis" {
		t.Errorf("spliced step missing source origin, got %q", out[0].Origin)
	}
}

// TestExpandPlanIncludes_BoxPlan exercises the box arm: an `include: box:<qualified>` step splices
// the host-flattened base-chain plan the resolved-project envelope carries in BoxPlans.
func TestExpandPlanIncludes_BoxPlan(t *testing.T) {
	rp := &spec.ResolvedProject{
		BoxPlans: map[string][]spec.Step{
			"fedora.jupyter": {{Check: "jupyter server answers"}},
		},
	}
	out, err := expandPlanIncludes(rp, []spec.Step{{Include: "box:fedora.jupyter"}})
	if err != nil {
		t.Fatalf("expandPlanIncludes(box): %v", err)
	}
	if len(out) != 1 || out[0].Check != "jupyter server answers" {
		t.Fatalf("box include did not splice the flattened plan: %+v", out)
	}
	if out[0].Origin != "box:fedora.jupyter" {
		t.Errorf("spliced box step origin = %q, want box:fedora.jupyter", out[0].Origin)
	}
	// A missing box is an authoring error, not a silent empty splice.
	if _, err := expandPlanIncludes(rp, []spec.Step{{Include: "box:nope"}}); err == nil {
		t.Error("expected an error for an unknown box include, got nil")
	}
}

// TestExpandPlanIncludes_PodTemplate exercises the pod arm (relocated from charly/check_include_test.go):
// a pod template stored OPAQUELY in Templates.Pod, decoded IN THE PLUGIN, spliced by `include: pod:<name>`.
func TestExpandPlanIncludes_PodTemplate(t *testing.T) {
	body, err := json.Marshal(&spec.Pod{Plan: []spec.Step{{Check: "inner pod probe"}}})
	if err != nil {
		t.Fatal(err)
	}
	rp := &spec.ResolvedProject{
		Templates: &spec.ProjectTemplates{Pod: map[string]spec.RawBody{"web": body}},
	}
	out, err := expandPlanIncludes(rp, []spec.Step{{Include: "pod:web"}})
	if err != nil {
		t.Fatalf("expandPlanIncludes(pod template): %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 spliced step from the pod plan, got %d: %+v", len(out), out)
	}
	if out[0].Check != "inner pod probe" {
		t.Errorf("spliced step is not the pod template's plan: %+v", out[0])
	}
	if out[0].Origin != "pod:web" {
		t.Errorf("spliced step origin = %q, want pod:web", out[0].Origin)
	}
}

// TestResolveIterateSandbox exercises the relocated sandbox classifier over the envelope Deploy
// tree: a bare sandbox → host; an ssh-venue deploy → vm; a host-rooted deploy → host; the default
// container venue → pod; an unknown name → pod.
func TestResolveIterateSandbox(t *testing.T) {
	rp := &spec.ResolvedProject{
		Deploy: map[string]*spec.Deploy{
			"vm-sandbox":   {Descent: &spec.DescentDescriptor{Venue: "ssh"}},
			"host-sandbox": {Descent: &spec.DescentDescriptor{HostRooted: true}},
			"pod-sandbox":  {Descent: &spec.DescentDescriptor{Venue: "container"}},
		},
	}
	cases := []struct {
		sandbox  string
		wantKind string
		wantName string
	}{
		{"", targetKindHost, ""},
		{"vm-sandbox", targetKindVM, "vm-sandbox"},
		{"host-sandbox", targetKindHost, "host-sandbox"},
		{"pod-sandbox", targetKindPod, "pod-sandbox"},
		{"absent", targetKindPod, "absent"},
	}
	for _, c := range cases {
		gk, gn := resolveIterateSandbox(rp, c.sandbox)
		if gk != c.wantKind || gn != c.wantName {
			t.Errorf("resolveIterateSandbox(%q) = (%q,%q), want (%q,%q)", c.sandbox, gk, gn, c.wantKind, c.wantName)
		}
	}
}
