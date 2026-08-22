package preempt

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/opencharly/sdk"
	"github.com/opencharly/spec/spec"
)

// gpu_imply.go — the GPU-implied-shared-consumer logic (K-wave W3a A2), relocated from
// charly/gpu_imply.go. Its former disk-backed wrapper (withImpliedGPUShared, LoadUnified-coupled
// via charly/preempt.go's gatherResources — gatherResources survives only for gpu_allocate.go's
// bedGPUPrereqMissing, K-wave 2 cone CONTESTED) is GONE: every acquire-side caller
// (candy/plugin-check's bed_session, candy/plugin-vm's vm_arbiter_shim, candy/plugin-fleet's
// handleLifecycleSimple) now ALWAYS dispatches an acquire-shared action to this compiled-in
// plugin (even with zero explicit shared tokens — cheap, in-proc, never a real RPC round trip),
// projecting the claimant node's GPU-relevant traits onto spec.ArbiterInvokeInput
// (IsGroup/IsPodMember/SecurityDevices — see spec/schema/arbiter.cue). invokeArbiter's
// AcquireShared case (arbiter.go) unions the implied token computed here onto the explicit tokens
// BEFORE calling AcquireShared, so "arbiter policy" (early-return-on-empty) lives entirely in the
// arbiter, not the deleted in-core proxy.
//
// IsGroup/IsPodMember are pre-derived CORE-SIDE (spec.FleetNode.IsGroup() + the former in-core
// isPodMember — the core-side copies are DELETED, K-wave 2 cone CONTESTED; the surviving
// fleet.IsContainerVenue predicate the wire projection uses stays); this file receives them as
// plain booleans on the wire, never re-derives them from a FleetNode. detectGPU below reaches
// the SAME candy/plugin-gpu detection primitive charly-core's own DetectGPU (gpu_shim.go) calls,
// via the EXISTING plugin-to-plugin InvokeProvider(ClassVerb,"gpu",...) peer-dispatch pattern
// pluginSwitchMode (holder_dispatch.go) already proves live in this exact package — no new seam.

// nvidiaTokenFromResources returns the `resource:` token whose gpu selector matches the NVIDIA
// PCI vendor — the arbitration token the auto-detected nvidia GPU device maps onto. "" when no
// gpu-backed nvidia token is configured. Lowest token name wins on a degenerate multi-match.
// Byte-for-byte port of the former charly/gpu_imply.go (pure, resources injected).
func nvidiaTokenFromResources(resources map[string]*spec.ResolvedResource) string {
	best := ""
	for tok, rdef := range resources {
		if rdef != nil && rdef.Gpu != nil && spec.NormalizePCIVendor(rdef.Gpu.Vendor) == spec.NvidiaVendorID {
			if best == "" || tok < best {
				best = tok
			}
		}
	}
	return best
}

// securityDevicesListNvidia reports whether a security.devices list explicitly references the
// NVIDIA GPU (the CDI name or a /dev/nvidia* node). Port of the former
// nodeSecurityListsNvidiaDevice, taking the raw device list directly (the wire projection) rather
// than a spec.FleetNode.
func securityDevicesListNvidia(devices []string) bool {
	for _, d := range devices {
		if strings.Contains(d, "nvidia.com/gpu") || strings.HasPrefix(d, "/dev/nvidia") {
			return true
		}
	}
	return false
}

// nodeConsumesNvidiaGPU reports whether a deploy node WOULD receive the nvidia GPU device at
// bring-up, from its wire-projected traits. detectGPU() (the host HAS a usable nvidia GPU)
// implies consumption ONLY for a POD deploy — a pod auto-gets the nvidia GPU as a CDI device on a
// GPU host (the pod config-setup emits `--device nvidia.com/gpu=all`). A local/host/vm command
// deploy gets NO container device, so on a GPU workstation it consumes the GPU only when it
// EXPLICITLY lists an nvidia device in security.devices. A GROUP deploy root carries no workload
// container of its own, so it never auto-consumes the GPU either.
func nodeConsumesNvidiaGPU(ctx context.Context, exec *sdk.Executor, isGroup, isPodMember bool, securityDevices []string) bool {
	if isGroup {
		return false
	}
	if isPodMember {
		return detectGPU(ctx, exec) || securityDevicesListNvidia(securityDevices)
	}
	return securityDevicesListNvidia(securityDevices)
}

// detectGPU is the swappable GPU-presence probe (package-level var, mirrors charly-core's own
// former DetectGPU var in gpu_shim.go — same testability shape, ctx/exec-threaded here since the
// production body dispatches over InvokeProvider rather than a direct host syscall). Tests swap
// it with a fake (withDetectGPU, gpu_imply_test.go) so the implied-shared logic is exercised
// without a live candy/plugin-gpu round trip.
var detectGPU = func(ctx context.Context, exec *sdk.Executor) bool {
	return realDetectGPU(ctx, exec)
}

// impliedSharedToken returns the gpu-backed `resource:` token a node implicitly claims as SHARED
// because it consumes the auto-detected nvidia GPU device — "" when the node is not a GPU
// consumer or no gpu token is configured. Callers invoke this ONLY for the acquire-shared action,
// which core's acquireResourceForClaimant dispatches only when the claimant declares NO explicit
// requires_exclusive (preempt.go's branch order) — so, unlike the former
// charly/gpu_imply.go's impliedGPUSharedToken, this never needs its own RequiredExclusive guard.
func impliedSharedToken(ctx context.Context, exec *sdk.Executor, isGroup, isPodMember bool, securityDevices []string, resources map[string]*spec.ResolvedResource) string {
	if !nodeConsumesNvidiaGPU(ctx, exec, isGroup, isPodMember, securityDevices) {
		return ""
	}
	return nvidiaTokenFromResources(resources)
}

// unionImpliedToken appends the implied token onto tokens — a no-op copy when nothing is implied
// OR tokens already lists it. Pure (resources/detect injected via impliedSharedToken's callers),
// unit-testable without a live host.
func unionImpliedToken(tokens []string, implied string) []string {
	if implied == "" || slices.Contains(tokens, implied) {
		return tokens
	}
	return append(append([]string(nil), tokens...), implied)
}

// realDetectGPU checks whether an NVIDIA GPU is usable via CDI — the SAME primitive charly-core's
// own DetectGPU (gpu_shim.go) calls, reached here via the EXISTING plugin-to-plugin
// InvokeProvider(ClassVerb,"gpu",...) peer-dispatch pluginSwitchMode (holder_dispatch.go) already
// proves live in this package for a different gpu action — no new seam. Wired through the
// swappable detectGPU var above; call detectGPU(ctx, exec), never this directly.
func realDetectGPU(ctx context.Context, exec *sdk.Executor) bool {
	params, err := json.Marshal(spec.GpuProbeInput{Action: "detect-gpu"})
	if err != nil {
		fmt.Fprintf(os.Stderr, "preempt: detect-gpu: marshal: %v\n", err)
		return false
	}
	out, err := exec.InvokeProvider(ctx, "verb", "gpu", sdk.OpRun, params, nil, sdk.InvokeProviderOpts{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "preempt: detect-gpu: %v\n", err)
		return false
	}
	var reply spec.GpuProbeReply
	if len(out) > 0 {
		if uerr := json.Unmarshal(out, &reply); uerr != nil {
			fmt.Fprintf(os.Stderr, "preempt: detect-gpu: decode: %v\n", uerr)
			return false
		}
	}
	return reply.Bool
}
