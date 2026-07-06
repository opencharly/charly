package main

import (
	"strings"
	"testing"
)

// validate_preempt_test.go — core-side tests for the preempt helpers that STAY in core after the
// arbiter's C9 move: the node validator (ValidatePreemptibleOnNode) + the config-time GPU-consumer
// predicate (deployNodeSharesGPU, gpu_imply.go). The arbiter's own tests relocated to
// candy/plugin-preempt.

// A node may not claim a resource BOTH exclusively and shared (the arbiter dispatches on one or
// the other; the driver modes are mutually exclusive).
func TestValidate_BothExclusiveAndShared_Errors(t *testing.T) {
	node := BundleNode{
		RequiresExclusive: []string{"nvidia-gpu"},
		RequiresShared:    []string{"nvidia-gpu"},
	}
	errs := &ValidationError{}
	ValidatePreemptibleOnNode("bad", &node, errs)
	if !errs.HasErrors() || !strings.Contains(errs.Error(), "both") {
		t.Fatalf("expected a both-exclusive-and-shared validation error, got: %q", errs.Error())
	}
}

// deployNodeSharesGPU reports whether a deploy node claims a SHARED resource backed by a gpu
// selector — so config_image emits the CDI `--device` even while the card is still vfio-bound.
func TestDeployNodeSharesGPU(t *testing.T) {
	resources := map[string]*ResolvedResource{
		"nvidia-gpu": {Gpu: &ResolvedGpuSelector{Vendor: "0x10de"}},
		"abstract":   {}, // no gpu selector
	}
	cases := []struct {
		name string
		node BundleNode
		want bool
	}{
		{"gpu-backed shared token", BundleNode{RequiresShared: []string{"nvidia-gpu"}}, true},
		{"selector-less shared token", BundleNode{RequiresShared: []string{"abstract"}}, false},
		{"no shared claim", BundleNode{}, false},
		{"exclusive is not shared", BundleNode{RequiresExclusive: []string{"nvidia-gpu"}}, false},
	}
	for _, tc := range cases {
		if got := deployNodeSharesGPU(tc.node, resources); got != tc.want {
			t.Errorf("%s: deployNodeSharesGPU = %v, want %v", tc.name, got, tc.want)
		}
	}
}
