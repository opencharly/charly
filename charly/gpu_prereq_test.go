package main

import (
	"testing"

	"github.com/opencharly/sdk/spec"
)

// TestGPUPrereqMissing proves the pre-build GPU fail-fast: a bed whose
// requires_exclusive/requires_shared token maps to a GPU-vendor resource absent
// on this host is reported missing (→ a clean SKIP), while a present vendor, a
// non-GPU token, or no token is not.
func TestGPUPrereqMissing(t *testing.T) {
	resources := map[string]*ResolvedResource{
		"nvidia-gpu": {Gpu: &ResolvedGpuSelector{Vendor: "0x10de"}},
		"test-lock":  {}, // a non-GPU arbitration token
	}
	nvidiaHost := func() spec.VFIOReport {
		return spec.VFIOReport{GPUs: []spec.VFIOGpu{{VFIOPCIDevice: spec.VFIOPCIDevice{VendorID: "0x10de"}}}}
	}
	amdOnlyHost := func() spec.VFIOReport {
		return spec.VFIOReport{GPUs: []spec.VFIOGpu{{VFIOPCIDevice: spec.VFIOPCIDevice{VendorID: "0x1002"}}}}
	}
	detectPanics := func() spec.VFIOReport { t.Fatal("detect must not run for a non-GPU bed"); return spec.VFIOReport{} }

	cases := []struct {
		name       string
		tokens     []string
		detect     func() spec.VFIOReport
		wantMiss   bool
		wantToken  string
		wantVendor string
	}{
		{"nvidia-required-absent", []string{"nvidia-gpu"}, amdOnlyHost, true, "nvidia-gpu", "0x10de"},
		{"nvidia-required-present", []string{"nvidia-gpu"}, nvidiaHost, false, "", ""},
		{"non-gpu-token-never-probes", []string{"test-lock"}, detectPanics, false, "", ""},
		{"no-tokens", nil, detectPanics, false, "", ""},
		{"unknown-token-never-probes", []string{"nope"}, detectPanics, false, "", ""},
		{"mixed-gpu-absent", []string{"test-lock", "nvidia-gpu"}, amdOnlyHost, true, "nvidia-gpu", "0x10de"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tok, vendor, miss := gpuPrereqMissing(tc.tokens, resources, tc.detect)
			if miss != tc.wantMiss || tok != tc.wantToken || vendor != tc.wantVendor {
				t.Errorf("got (%q,%q,%v) want (%q,%q,%v)", tok, vendor, miss, tc.wantToken, tc.wantVendor, tc.wantMiss)
			}
		})
	}
}
