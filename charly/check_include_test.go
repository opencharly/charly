package main

import (
	"encoding/json"
	"testing"
)

// TestExpandPlanIncludes_PodTemplate exercises the pod-template include branch
// through the REAL candy/plugin-substrate OpResolve dispatch (the pod-template
// de-type, Cutover J): a pod template stored OPAQUELY in uf.Pod, spliced by an
// `include: pod:<name>` step. Without the resolve leg the pod branch cannot read
// the opaque body's Plan, so this fails.
func TestExpandPlanIncludes_PodTemplate(t *testing.T) {
	body, err := json.Marshal(&PodSpec{Plan: []Step{{Check: "inner pod probe"}}})
	if err != nil {
		t.Fatal(err)
	}
	uf := &UnifiedFile{Pod: map[string]json.RawMessage{"web": body}}
	out, err := ExpandPlanIncludes(uf, nil, []Step{{Include: "pod:web"}})
	if err != nil {
		t.Fatalf("ExpandPlanIncludes(pod template): %v", err)
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
