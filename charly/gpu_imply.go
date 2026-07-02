package main

import (
	"slices"
	"strings"
)

// gpu_imply.go — the CONFIG-COUPLED GPU-consumer helpers that STAY in core (cutover C9).
//
// The GPU DRIVER-SWITCH primitive (the vfio<->nvidia sysfs rebind) moved into candy/plugin-gpu
// (see gpu_shim.go's driver-switch shims). What REMAINS host-side is the logic that reads the
// project config (BundleNode / ResourceDef) to decide whether a deploy CONSUMES the nvidia GPU
// — used by two in-core consumers that cannot move: config_image.go (whether to emit the CDI
// `--device nvidia.com/gpu=all` on a pod at bring-up) and the arbiter's acquire shim
// (withImpliedGPUShared auto-promotes a GPU-consuming pod to a SHARED claimant). These operate
// on the package-main config types + the DetectGPU shim, so they stay in core.

// deployNodeSharesGPU reports whether a deploy node claims a SHARED resource backed by a gpu
// selector — so it must get the GPU device (`--device nvidia.com/gpu=all` via CDI) in its
// quadlet/run args EVEN when the host card is currently vfio-bound, because the arbiter flips
// it to nvidia at start.
func deployNodeSharesGPU(node BundleNode, resources map[string]*ResourceDef) bool {
	for _, tok := range node.RequiredShared() {
		if rdef := resources[tok]; rdef != nil && rdef.Gpu != nil {
			return true
		}
	}
	return false
}

// nvidiaTokenFromResources returns the `resource:` token whose gpu selector matches the NVIDIA
// PCI vendor — the arbitration token the auto-detected nvidia GPU device maps onto. "" when no
// gpu-backed nvidia token is configured. Lowest token name wins on a degenerate multi-match.
func nvidiaTokenFromResources(resources map[string]*ResourceDef) string {
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
func nodeSecurityListsNvidiaDevice(node BundleNode) bool {
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
// bring-up: either the host presents a usable nvidia GPU (DetectGPU — the same signal
// config_image uses), or the node explicitly lists an nvidia device in security.devices.
func nodeConsumesNvidiaGPU(node BundleNode) bool {
	return DetectGPU() || nodeSecurityListsNvidiaDevice(node)
}

// impliedGPUSharedToken returns the gpu-backed `resource:` token a node implicitly claims as
// SHARED because it consumes the auto-detected nvidia GPU device — "" when the node is not a
// GPU consumer, claims a resource exclusively, or no gpu token is configured.
func impliedGPUSharedToken(node BundleNode, resources map[string]*ResourceDef) string {
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
func applyImpliedGPUShared(node BundleNode, resources map[string]*ResourceDef) BundleNode {
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
func withImpliedGPUShared(node BundleNode) BundleNode {
	return applyImpliedGPUShared(node, gatherResources())
}
