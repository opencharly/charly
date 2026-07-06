package main

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// GPU auto-allocation: turn a VM-targeted claimant's `requires_exclusive:`
// token into a concrete PCI `<hostdev>` block, automatically, at
// `charly vm create`. The token → vendor mapping is YAML config (the embedded
// `resource:` vocabulary in charly/charly.yml); the device is discovered live
// via DetectVFIO. This replaces
// the manual `charly vm gpu list` → paste-into-instance.yml workflow.
//
// Operator directive: "if a resource is required either add it automatically
// or fail hard." So: a required GPU resource with a matching card on the host
// is auto-allocated + persisted; a required GPU resource with NO matching card
// is a HARD ERROR (never a silent GPU-less boot).

// normalizePCIVendor + selectGPUByVendor moved to package spec (spec.NormalizePCIVendor /
// spec.SelectGPUByVendor), aliased in gpu_shim.go — shared with candy/plugin-gpu's driver-switch
// (cutover C9, R3). Auto-allocation below uses the aliases.

// vfioGpuToHostdevs converts a GPU's IOMMU-group members into the STRUCTURED
// libvirt hostdev form (managed='yes' — libvirt binds each function to
// vfio-pci on VM start and rebinds the host driver on stop). This is the
// single source of "which devices + their PCI fields"; renderHostdevsBlock
// (the `charly vm gpu list` text emitter) consumes it, so the YAML-text and
// structured paths cannot drift (R3).
func vfioGpuToHostdevs(members []VFIOPCIDevice) []LibvirtHostdev {
	var out []LibvirtHostdev
	for _, m := range members {
		dom, bus, slot, fn, ok := parsePCIAddr(m.Addr)
		if !ok {
			continue
		}
		out = append(out, LibvirtHostdev{
			Type:    "pci",
			Managed: "yes",
			Source: map[string]string{
				"domain":   dom,
				"bus":      bus,
				"slot":     slot,
				"function": fn,
			},
		})
	}
	return out
}

// specHasHostdev / ovrHasHostdev report whether a committed vm.yml spec or a
// per-host instance.yml override already carries a hostdev — in which case
// auto-allocation defers to it (operator authority; no double-inject).
func specHasHostdev(spec *VmSpec) bool {
	return spec != nil && spec.Libvirt != nil && spec.Libvirt.Devices != nil &&
		len(spec.Libvirt.Devices.Hostdevs) > 0
}

func ovrHasHostdev(ovr *VmInstanceOverride) bool {
	return ovr != nil && ovr.Libvirt != nil && ovr.Libvirt.Devices != nil &&
		len(ovr.Libvirt.Devices.Hostdevs) > 0
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

// autoAllocateExclusiveGPUs is the create-time hook. When the VM's claimant
// (the deploy/bed that references it via requires_exclusive) needs a GPU
// resource defined in the embedded `resource:` vocabulary (charly/charly.yml),
// it:
//
//   - defers to any operator-authored hostdev (vm.yml spec OR instance.yml
//     overlay) — no double-inject, no re-detection;
//   - else requires backend libvirt (PCI <hostdev> renders only there);
//   - else DetectVFIO + selectGPUByVendor → on hit, persist the whole
//     IOMMU-group hostdev block into the per-host instance.yml (visible +
//     stable) and return the updated override for ApplyToVmSpec to inject;
//   - on miss, FAIL HARD (the resource is required but unsatisfiable).
//
// ovr is the already-loaded per-host override (may be nil). Returns the
// (possibly newly-created) override so the caller can ApplyToVmSpec it.
func autoAllocateExclusiveGPUs(spec *VmSpec, ovr *VmInstanceOverride, cnode *BundleNode, resources map[string]*ResolvedResource, domainName, backend string) (*VmInstanceOverride, error) {
	tok, sel, ok := requiredGPUResource(cnode, resources)
	if !ok {
		return ovr, nil
	}
	vendor := normalizePCIVendor(sel.Vendor)

	// Operator-authored hostdev wins (committed vm.yml or instance.yml). Per
	// the locked design: persisted block is treated as authoritative — delete
	// it to force re-detection.
	if specHasHostdev(spec) || ovrHasHostdev(ovr) {
		fmt.Printf("note: %s requires exclusive resource %q; a <hostdev> is already configured — using it (no GPU auto-allocation)\n", domainName, tok)
		return ovr, nil
	}

	// A PCI <hostdev> only renders on the libvirt backend.
	if backend != "" && backend != "libvirt" {
		return ovr, fmt.Errorf("%s requires exclusive GPU resource %q but backend is %q — GPU passthrough needs `backend: libvirt` on the kind:vm entity (PCI <hostdev> does not render under qemu)", domainName, tok, backend)
	}

	rep := DetectVFIO()
	gpu, found := selectGPUByVendor(rep, vendor)
	if !found {
		return ovr, fmt.Errorf("%s requires exclusive GPU resource %q (gpu vendor %s) but no matching GPU was found on this host — check `charly vm gpu status` (card present? IOMMU enabled? bound to vfio-pci?)", domainName, tok, vendor)
	}

	hostdevs := vfioGpuToHostdevs(gpu.GroupMembers)
	if len(hostdevs) == 0 {
		return ovr, fmt.Errorf("%s: GPU %s (resource %q) yielded no passthrough functions — unparsable PCI addresses", domainName, gpu.Addr, tok)
	}

	if ovr == nil {
		ovr = &VmInstanceOverride{}
	}
	if ovr.Libvirt == nil {
		ovr.Libvirt = &LibvirtDomain{}
	}
	if ovr.Libvirt.Devices == nil {
		ovr.Libvirt.Devices = &LibvirtDevices{}
	}
	ovr.Libvirt.Devices.Hostdevs = hostdevs

	header := fmt.Sprintf(
		"# Auto-allocated by `charly vm create` for requires_exclusive: [%s].\n"+
			"# resource %q -> gpu vendor %s -> %s (IOMMU group %d, %d function(s)).\n"+
			"# Operator edits below OVERRIDE auto-allocation; delete the hostdevs block to re-detect.\n",
		tok, tok, vendor, gpu.Addr, gpu.IOMMUGroup, len(hostdevs))
	if err := writeInstanceOverrideHostdevs(domainName, ovr, header); err != nil {
		return ovr, fmt.Errorf("persisting auto-allocated GPU hostdev for %s: %w", domainName, err)
	}
	fmt.Printf("auto-allocated GPU resource %q -> %s (%s) IOMMU group %d -> %d <hostdev> injected into %s\n",
		tok, gpu.Addr, vendor, gpu.IOMMUGroup, len(hostdevs), domainName)
	return ovr, nil
}

// writeInstanceOverrideHostdevs persists the override (with its freshly-set
// hostdevs) to the per-host instance.yml, preserving disposable/lifecycle and
// any operator filesystems already present in the in-memory override. A header
// comment records the auto-allocation provenance for the human reader.
func writeInstanceOverrideHostdevs(domainName string, ovr *VmInstanceOverride, header string) error {
	path, err := VmInstanceOverridePath(domainName)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(ovr)
	if err != nil {
		return err
	}
	return os.WriteFile(path, append([]byte(header), data...), 0o644)
}
