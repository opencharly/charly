package main

import "os"

// gpu_allocate.go — the core-side GPU-resource PREREQ + host-probe helpers that
// survive the P10 VM-CLI move. The create-time auto-allocation pipeline
// (autoAllocateExclusiveGPUs + vfioGpuToHostdevs + the instance-override
// persistence) moved into candy/plugin-vm with the `charly vm create` handler;
// what stays are the pure resource-vocabulary predicates the bed runner
// (check_bed_run.go — bedGPUPrereqMissing), the preempt validator
// (validate_preempt.go — requiredGPUResource), and the host-probe seam
// (host_build_hostprobe.go — vfioPciAvailable) still call.

// bedGPUPrereqMissing reports whether a bed claims a host GPU resource — via
// requires_exclusive OR requires_shared — whose vendor has NO matching card on
// this host. It is the pre-build fail-fast that turns an unsatisfiable GPU bed
// into a clean SKIP (like a missing /dev/kvm) instead of a 10-30 GB image build
// that dies at start with "unresolvable CDI devices nvidia.com/gpu=all". Detects
// host VFIO once, lazily, only when a GPU-selector token is actually present.
// Returns the token, normalized vendor, and missing=true on the first
// unsatisfiable GPU resource; false when the bed needs no GPU resource (or every
// required vendor is present, or the resource vocabulary is unreadable — never
// skip on a detection gap, only on a definite absence).
func bedGPUPrereqMissing(node BundleNode) (token, vendor string, missing bool) {
	resources := gatherResources()
	if len(resources) == 0 {
		return "", "", false
	}
	tokens := append(dedupeNonEmpty(node.RequiredExclusive()), dedupeNonEmpty(node.RequiredShared())...)
	return gpuPrereqMissing(tokens, resources, DetectVFIO)
}

// gpuPrereqMissing is the pure decision behind bedGPUPrereqMissing: given the
// claimant's resource tokens, the resolved resource vocabulary, and a
// lazily-invoked host VFIO detector, return the first token whose GPU vendor has
// no matching card. detect is called AT MOST ONCE, only when a GPU-selector
// token is actually present (so a non-GPU bed never probes hardware).
func gpuPrereqMissing(tokens []string, resources map[string]*ResolvedResource, detect func() VFIOReport) (token, vendor string, missing bool) {
	detected := false
	var rep VFIOReport
	for _, tok := range tokens {
		rdef := resources[tok]
		if rdef == nil || rdef.Gpu == nil {
			continue // token maps to no GPU-selector resource — not a GPU prereq
		}
		if !detected {
			rep = detect()
			detected = true
		}
		v := normalizePCIVendor(rdef.Gpu.Vendor)
		if _, found := selectGPUByVendor(rep, v); !found {
			return tok, v, true
		}
	}
	return "", "", false
}

// requiredGPUResource scans a claimant's requires_exclusive tokens for the
// first that maps to a `resource:` carrying a gpu selector. Returns the token,
// the selector, and ok=false when the claimant needs no GPU resource.
func requiredGPUResource(cnode *BundleNode, resources map[string]*ResolvedResource) (string, *ResolvedGpuSelector, bool) {
	if cnode == nil {
		return "", nil, false
	}
	for _, tok := range cnode.RequiredExclusive() {
		if rdef := resources[tok]; rdef != nil && rdef.Gpu != nil {
			return tok, rdef.Gpu, true
		}
	}
	return "", nil, false
}

// vfioPciAvailable reports whether the vfio-pci driver is present on the host.
func vfioPciAvailable() bool {
	for _, p := range []string{"/sys/bus/pci/drivers/vfio-pci", "/sys/module/vfio_pci"} {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}
