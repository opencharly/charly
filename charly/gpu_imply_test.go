package main

import (
	"slices"
	"testing"
)

// withDetectGPU swaps the package-level DetectGPU probe for the duration of a
// test (restored on cleanup), so the implied-shared logic can be exercised
// without a real nvidia-smi on the host.
func withDetectGPU(t *testing.T, present bool) {
	t.Helper()
	prev := DetectGPU
	DetectGPU = func() bool { return present }
	t.Cleanup(func() { DetectGPU = prev })
}

// gpuPodNode is a plain pod deploy that declares NO resource claim — the
// untracked GPU-consumer case from the RCA (auto-detected `--device
// nvidia.com/gpu=all`, no requires_shared).
func gpuPodNode() BundleNode { return BundleNode{Target: "pod"} }

// I1. A GPU-device node (host presents nvidia) implies requires_shared:[nvidia-gpu];
// a non-GPU node implies nothing — the core of the auto-claim fix.
func TestImpliedGPUShared_TokenFromDeviceUsage(t *testing.T) {
	res := gpuResources() // {nvidia-gpu: {gpu: {vendor: 0x10de}}}

	withDetectGPU(t, true)
	if tok := impliedGPUSharedToken(gpuPodNode(), res); tok != "nvidia-gpu" {
		t.Fatalf("a GPU-consuming pod must imply the nvidia-gpu token, got %q", tok)
	}

	withDetectGPU(t, false)
	if tok := impliedGPUSharedToken(gpuPodNode(), res); tok != "" {
		t.Fatalf("a non-GPU pod must imply no token, got %q", tok)
	}
}

// I2. The implied token is derived from the resource: config — a host with no
// gpu-backed token implies nothing even when the GPU is present.
func TestImpliedGPUShared_NoTokenWithoutResourceConfig(t *testing.T) {
	withDetectGPU(t, true)
	if tok := impliedGPUSharedToken(gpuPodNode(), nil); tok != "" {
		t.Fatalf("no resource config → no implied token, got %q", tok)
	}
	// A selector-less (abstract) token is not gpu-backed → not implied.
	abstract := map[string]*ResourceDef{"abstract": {}}
	if tok := impliedGPUSharedToken(gpuPodNode(), abstract); tok != "" {
		t.Fatalf("a selector-less token must not be implied, got %q", tok)
	}
}

// I3. A node-intrinsic /dev/nvidia* device declaration implies the token even
// when host auto-detection is momentarily false (card consumer regardless).
func TestImpliedGPUShared_SecurityDevicesSignal(t *testing.T) {
	withDetectGPU(t, false)
	node := BundleNode{Target: "pod", Security: &SecurityConfig{Devices: []string{"/dev/nvidia0"}}}
	if tok := impliedGPUSharedToken(node, gpuResources()); tok != "nvidia-gpu" {
		t.Fatalf("a node listing /dev/nvidia0 must imply the token, got %q", tok)
	}
	// The CDI device name is the other accepted form.
	node2 := BundleNode{Target: "pod", Security: &SecurityConfig{Devices: []string{"nvidia.com/gpu=all"}}}
	if tok := impliedGPUSharedToken(node2, gpuResources()); tok != "nvidia-gpu" {
		t.Fatalf("a node listing nvidia.com/gpu must imply the token, got %q", tok)
	}
}

// I4. An EXCLUSIVE claimant (a VM via vfio) is never treated as a pod GPU
// consumer — it gets a PCI hostdev, not the pod --device.
func TestImpliedGPUShared_ExclusiveClaimantNotImplied(t *testing.T) {
	withDetectGPU(t, true)
	if tok := impliedGPUSharedToken(claimantNode([]string{"nvidia-gpu"}), gpuResources()); tok != "" {
		t.Fatalf("an exclusive claimant must not imply a shared token, got %q", tok)
	}
}

// I5. applyImpliedGPUShared unions the token onto a bare node, and NEVER
// double-claims a token the node already lists.
func TestApplyImpliedGPUShared_UnionAndNoDoubleClaim(t *testing.T) {
	withDetectGPU(t, true)
	res := gpuResources()

	got := applyImpliedGPUShared(gpuPodNode(), res)
	if !slices.Equal(got.RequiresShared, []string{"nvidia-gpu"}) {
		t.Fatalf("bare GPU pod must gain requires_shared:[nvidia-gpu], got %v", got.RequiresShared)
	}

	// Already an explicit shared claimant → unchanged (no duplicate).
	explicit := sharedNode([]string{"nvidia-gpu"})
	got = applyImpliedGPUShared(explicit, res)
	if !slices.Equal(got.RequiresShared, []string{"nvidia-gpu"}) {
		t.Fatalf("must not double-claim an already-declared token, got %v", got.RequiresShared)
	}
}

// The end-to-end "implied GPU pod is preemptable" integration test lives in
// candy/plugin-preempt now (TestArbiter_ExclusivePreemptsShared, over the relocated seam-faked
// arbiter): the imply HALF (applyImpliedGPUShared → the gpu token, above) + the preemption HALF
// (an exclusive claim stops a shared holder) split across the C9 core↔plugin boundary, so the
// former combined TestArbiter_PreemptsImpliedSharedGPUPod is covered by those two halves.

// --- core test helpers (were in the relocated preempt_test.go / preempt_shared_test.go) ------

// gpuResources is the token map an implied-GPU test sees (drives the imply logic; core type).
func gpuResources() map[string]*ResourceDef {
	return map[string]*ResourceDef{"nvidia-gpu": {Gpu: &GpuSelector{Vendor: "0x10de"}}}
}

// sharedNode / claimantNode build a pod deploy declaring a SHARED / EXCLUSIVE claim.
func sharedNode(tokens []string) BundleNode { return BundleNode{Target: "pod", RequiresShared: tokens} }
func claimantNode(tokens []string) BundleNode {
	return BundleNode{Target: "pod", RequiresExclusive: tokens}
}
