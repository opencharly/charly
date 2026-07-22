package main

import (
	"slices"
	"strings"

	"github.com/opencharly/sdk/spec"
)

// gpu_imply.go — the CONFIG-COUPLED GPU-consumer helpers that STAY in core (cutover C9).
//
// The GPU DRIVER-SWITCH primitive (the vfio<->nvidia sysfs rebind) moved into candy/plugin-gpu
// (see gpu_shim.go's driver-switch shims). What REMAINS host-side is the logic that reads the
// project config (BundleNode / ResourceDef) to decide whether a deploy CONSUMES the nvidia GPU
// — used by the arbiter's acquire shim (withImpliedGPUShared auto-promotes a GPU-consuming pod
// to a SHARED claimant). This operates on the package-main config types + the DetectGPU shim, so
// it stays in core. (Its former sibling deployNodeSharesGPU, and its claimed config_image.go
// consumer, was a dead-code-radical-removal-batch deletion — zero real callers; config_image.go
// never called it.)

// nvidiaTokenFromResources returns the `resource:` token whose gpu selector matches the NVIDIA
// PCI vendor — the arbitration token the auto-detected nvidia GPU device maps onto. "" when no
// gpu-backed nvidia token is configured. Lowest token name wins on a degenerate multi-match.
func nvidiaTokenFromResources(resources map[string]*ResolvedResource) string {
	best := ""
	for tok, rdef := range resources {
		if rdef != nil && rdef.Gpu != nil && normalizePCIVendor(rdef.Gpu.Vendor) == nvidiaVendorID {
			if best == "" || tok < best {
				best = tok
			}
		}
	}
	return best
}

// nodeSecurityListsNvidiaDevice reports whether a node's security.devices explicitly references
// the NVIDIA GPU (the CDI name or a /dev/nvidia* node).
func nodeSecurityListsNvidiaDevice(node spec.BundleNode) bool {
	if node.Security == nil {
		return false
	}
	for _, d := range node.Security.Devices {
		if strings.Contains(d, "nvidia.com/gpu") || strings.HasPrefix(d, "/dev/nvidia") {
			return true
		}
	}
	return false
}

// nodeConsumesNvidiaGPU reports whether a deploy node WOULD receive the nvidia GPU device at
// bring-up. DetectGPU() (the host HAS a usable nvidia GPU) implies consumption ONLY for a POD
// deploy — a pod auto-gets the nvidia GPU as a CDI device on a GPU host (config_image emits
// `--device nvidia.com/gpu=all`). A local/host/vm command deploy gets NO container device, so on a
// GPU workstation it consumes the GPU only when it EXPLICITLY lists an nvidia device in
// security.devices. Without this pod gate, EVERY local command bed on a GPU host would wrongly
// acquire an implied nvidia-GPU-shared lease (which broke check-preempt-local's clean-ledger
// `charly preempt status` assertion — the bed held its OWN implied lease).
func nodeConsumesNvidiaGPU(node spec.BundleNode) bool {
	// A GROUP deploy root carries no workload container of its own (it only groups
	// sibling members), so config_image emits NO `--device nvidia.com/gpu=all` for it
	// — it never auto-consumes the GPU. Without this gate a group bed root on a GPU host
	// wrongly acquires an implied nvidia-gpu-shared lease (isPodMember treats its empty
	// Target as a pod), which broke check-preempt-live-pod: the bed root held an
	// `nvidia-gpu` lease instead of surfacing the members' authored test-lock preemption.
	if node.IsGroup() {
		return false
	}
	if isPodMember(&node) {
		return DetectGPU() || nodeSecurityListsNvidiaDevice(node)
	}
	return nodeSecurityListsNvidiaDevice(node)
}

// impliedGPUSharedToken returns the gpu-backed `resource:` token a node implicitly claims as
// SHARED because it consumes the auto-detected nvidia GPU device — "" when the node is not a
// GPU consumer, claims a resource exclusively, or no gpu token is configured.
func impliedGPUSharedToken(node spec.BundleNode, resources map[string]*ResolvedResource) string {
	if len(node.RequiredExclusive()) > 0 {
		return ""
	}
	if !nodeConsumesNvidiaGPU(node) {
		return ""
	}
	return nvidiaTokenFromResources(resources)
}

// applyImpliedGPUShared returns node with its RequiresShared unioned with the implied gpu
// token — a no-op copy when nothing is implied OR the node already claims the token. Pure
// (resources injected) so it is unit-testable without disk.
func applyImpliedGPUShared(node spec.BundleNode, resources map[string]*ResolvedResource) spec.BundleNode {
	tok := impliedGPUSharedToken(node, resources)
	if tok == "" || slices.Contains(node.RequiresShared, tok) {
		return node
	}
	node.RequiresShared = append(append([]string(nil), node.RequiresShared...), tok)
	return node
}

// withImpliedGPUShared is the disk-backed wrapper used at the single arbiter-claim entry point
// (acquireResourceForClaimant): it loads the project resource map and unions the implied gpu
// token onto node, so a GPU-consuming pod that declared NO explicit claim still acquires a
// shared lease and becomes preemptable by an exclusive claimant.
func withImpliedGPUShared(node spec.BundleNode) spec.BundleNode {
	return applyImpliedGPUShared(node, gatherResources())
}
