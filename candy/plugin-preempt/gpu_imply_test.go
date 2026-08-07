package preempt

import (
	"context"
	"testing"

	"github.com/opencharly/sdk"
	"github.com/opencharly/spec/spec"
)

// gpu_imply_test.go — ported from the former charly/gpu_imply_test.go (K-wave W3a A2). The pure
// decision functions moved with their subject (gpu_imply.go); test fixtures switched from a full
// spec.FleetNode to the wire-projected (isGroup, isPodMember, securityDevices) triple the
// acquire-side callers send (the former in-core acquireDispatch shims are DELETED, K-wave 2 cone
// CONTESTED), mirroring the production shape exactly.

// withDetectGPU swaps the package-level detectGPU probe for the duration of a test (restored on
// cleanup), so the implied-shared logic can be exercised without a live candy/plugin-gpu round
// trip. Ports the former core-side withDetectGPU 1:1 (var-swap testability, unchanged shape).
func withDetectGPU(t *testing.T, present bool) {
	t.Helper()
	prev := detectGPU
	detectGPU = func(context.Context, *sdk.Executor) bool { return present }
	t.Cleanup(func() { detectGPU = prev })
}

// rawGpuResources is the token map an implied-GPU test sees (drives the imply logic; core type).
func rawGpuResources() map[string]*spec.ResolvedResource {
	return map[string]*spec.ResolvedResource{"nvidia-gpu": {Gpu: &spec.ResolvedGpuSelector{Vendor: "0x10de"}}}
}

// I1. A GPU-device pod claimant (host presents nvidia) implies the nvidia-gpu token; a non-GPU
// pod claimant implies nothing — the core of the auto-claim fix.
func TestImpliedSharedToken_TokenFromDeviceUsage(t *testing.T) {
	res := rawGpuResources()

	withDetectGPU(t, true)
	if tok := impliedSharedToken(context.Background(), nil, false, true, nil, res); tok != "nvidia-gpu" {
		t.Fatalf("a GPU-consuming pod must imply the nvidia-gpu token, got %q", tok)
	}

	withDetectGPU(t, false)
	if tok := impliedSharedToken(context.Background(), nil, false, true, nil, res); tok != "" {
		t.Fatalf("a non-GPU pod must imply no token, got %q", tok)
	}
}

// I2. The implied token is derived from the resource: config — a host with no gpu-backed token
// implies nothing even when the GPU is present.
func TestImpliedSharedToken_NoTokenWithoutResourceConfig(t *testing.T) {
	withDetectGPU(t, true)
	if tok := impliedSharedToken(context.Background(), nil, false, true, nil, nil); tok != "" {
		t.Fatalf("no resource config → no implied token, got %q", tok)
	}
	// A selector-less (abstract) token is not gpu-backed → not implied.
	abstract := map[string]*spec.ResolvedResource{"abstract": {}}
	if tok := impliedSharedToken(context.Background(), nil, false, true, nil, abstract); tok != "" {
		t.Fatalf("a selector-less token must not be implied, got %q", tok)
	}
}

// I3. A node-intrinsic /dev/nvidia* device declaration implies the token even when host
// auto-detection is momentarily false (card consumer regardless).
func TestImpliedSharedToken_SecurityDevicesSignal(t *testing.T) {
	withDetectGPU(t, false)
	if tok := impliedSharedToken(context.Background(), nil, false, true, []string{"/dev/nvidia0"}, rawGpuResources()); tok != "nvidia-gpu" {
		t.Fatalf("a node listing /dev/nvidia0 must imply the token, got %q", tok)
	}
	// The CDI device name is the other accepted form.
	if tok := impliedSharedToken(context.Background(), nil, false, true, []string{"nvidia.com/gpu=all"}, rawGpuResources()); tok != "nvidia-gpu" {
		t.Fatalf("a node listing nvidia.com/gpu must imply the token, got %q", tok)
	}
}

// I4b. The detectGPU (host-has-a-GPU) implied-share applies ONLY to a POD deploy — a local/host
// command deploy (isPodMember=false) gets NO container device, so on a GPU host it is NOT an
// implied GPU consumer (only an explicit nvidia device makes it one). Guards the fix that stopped
// every local command bed on a GPU workstation from wrongly acquiring an implied
// nvidia-gpu-shared lease — which held the bed's OWN lease and broke check-preempt-local's "No
// active preemption leases." status assertion.
func TestImpliedSharedToken_LocalDeployNotImpliedOnGPUHost(t *testing.T) {
	withDetectGPU(t, true) // host HAS a GPU
	res := rawGpuResources()
	// A local command deploy (isPodMember=false) with NO explicit nvidia device → NOT a consumer.
	if tok := impliedSharedToken(context.Background(), nil, false, false, nil, res); tok != "" {
		t.Fatalf("a local command deploy on a GPU host must NOT imply the nvidia-gpu token, got %q", tok)
	}
	// The explicit-device path survives for a local deploy that really lists the nvidia device.
	if tok := impliedSharedToken(context.Background(), nil, false, false, []string{"nvidia.com/gpu=all"}, res); tok != "nvidia-gpu" {
		t.Fatalf("a local deploy explicitly listing the nvidia device MUST imply the token, got %q", tok)
	}
	// The pod path is unchanged: a pod on a GPU host still implies the token.
	if tok := impliedSharedToken(context.Background(), nil, false, true, nil, res); tok != "nvidia-gpu" {
		t.Fatalf("a pod on a GPU host must still imply the nvidia-gpu token, got %q", tok)
	}
}

// I4c. A GROUP deploy root (no workload container, only sibling members) on a GPU host must NOT
// imply the nvidia-gpu token — the pod config-setup emits no CDI device for a group. Regression:
// check-preempt-live-pod's group root wrongly held an implied nvidia-gpu lease, masking the
// members' authored test-lock preemption.
func TestImpliedSharedToken_GroupRootNotImpliedOnGPUHost(t *testing.T) {
	withDetectGPU(t, true) // host HAS a GPU
	res := rawGpuResources()
	// isGroup=true wins regardless of isPodMember (mirrors node.IsGroup() short-circuiting
	// nodeConsumesNvidiaGPU before the isPodMember branch runs).
	if tok := impliedSharedToken(context.Background(), nil, true, true, nil, res); tok != "" {
		t.Fatalf("a group root on a GPU host must NOT imply the nvidia-gpu token, got %q", tok)
	}
	// The pod path is unchanged: a pod on a GPU host still implies the token.
	if tok := impliedSharedToken(context.Background(), nil, false, true, nil, res); tok != "nvidia-gpu" {
		t.Fatalf("a pod on a GPU host must still imply the nvidia-gpu token, got %q", tok)
	}
}

// I5. unionImpliedToken unions the token onto a bare tokens list, and NEVER double-claims a token
// already listed.
func TestUnionImpliedToken_UnionAndNoDoubleClaim(t *testing.T) {
	got := unionImpliedToken(nil, "nvidia-gpu")
	if len(got) != 1 || got[0] != "nvidia-gpu" {
		t.Fatalf("bare tokens must gain nvidia-gpu, got %v", got)
	}
	// Already listed → unchanged (no duplicate).
	got = unionImpliedToken([]string{"nvidia-gpu"}, "nvidia-gpu")
	if len(got) != 1 || got[0] != "nvidia-gpu" {
		t.Fatalf("must not double-claim an already-declared token, got %v", got)
	}
	// No implied token → tokens unchanged.
	if got := unionImpliedToken([]string{"other"}, ""); len(got) != 1 || got[0] != "other" {
		t.Fatalf("empty implied token must leave tokens unchanged, got %v", got)
	}
}

// The end-to-end "implied GPU pod is preemptable" integration test lives in this package's
// arbiter_test.go (TestArbiter_ExclusivePreemptsShared, over the seam-faked arbiter): the imply
// HALF (impliedSharedToken → the gpu token, above) + the preemption HALF (an exclusive claim
// stops a shared holder) split across the C9 core↔plugin boundary, so the former combined
// TestArbiter_PreemptsImpliedSharedGPUPod is covered by those two halves.
