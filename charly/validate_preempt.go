package main

import (
	"fmt"
	"strings"

	"github.com/opencharly/sdk/spec"
)

// Validation for the resource-arbitration ("preemptible") classification axis
// — the holder-side `preemptible:` block and the claimant-side
// `requires_exclusive:` list on a BundleNode. See classification.go +
// charly/preempt.go. Mirrors validate_ephemeral.go.

// ValidatePreemptibleOnNode checks one deploy node's preemptible +
// requires_exclusive fields and accumulates problems into errs:
//
//   - preemptible.holds must be non-empty (a holder that holds nothing is
//     meaningless — nothing for a claimant to contend over).
//   - preemptible.stop must be "shutdown" (the ONLY mechanism that frees a
//     VFIO passthrough device; pause/managedsave/destroy are rejected with a
//     reason).
//   - preemptible.restore must be "always" or "on-success".
//   - requires_exclusive entries must be non-empty strings.
//   - a node may not both hold and require the SAME token (self-contention).
func ValidatePreemptibleOnNode(name string, node *spec.BundleNode, errs *ValidationError) {
	if node == nil {
		return
	}
	if p := node.Preemptible; p != nil {
		if len(dedupeNonEmpty(p.Holds)) == 0 {
			errs.Add("deploy %q: `preemptible.holds` must list at least one exclusive-resource token — a preemptible holder that holds nothing is meaningless", name)
		}
		if p.Stop != "" && p.Stop != spec.PreemptStopShutdown {
			errs.Add("deploy %q: `preemptible.stop: %s` is not supported — only %q (graceful shutdown, disk preserved) frees a passthrough device; pause/managedsave keep the device assigned to the holder", name, p.Stop, spec.PreemptStopShutdown)
		}
		if p.Restore != "" && p.Restore != spec.PreemptRestoreAlways && p.Restore != spec.PreemptRestoreSuccess {
			errs.Add("deploy %q: `preemptible.restore: %s` is invalid — must be %q or %q", name, p.Restore, spec.PreemptRestoreAlways, spec.PreemptRestoreSuccess)
		}
	}
	for _, tok := range node.RequiresExclusive {
		if strings.TrimSpace(tok) == "" {
			errs.Add("deploy %q: `requires_exclusive` contains an empty token", name)
		}
	}
	for _, tok := range node.RequiresShared {
		if strings.TrimSpace(tok) == "" {
			errs.Add("deploy %q: `requires_shared` contains an empty token", name)
		}
	}
	// A node claims a resource EITHER exclusively (sole use — a VM) OR shared
	// (refcounted — pods), never both: the arbiter dispatches on whichever list
	// is set (acquireResourceForClaimant), and the driver MODE a resource is in
	// (vfio for exclusive, nvidia for shared) is mutually exclusive.
	if len(node.RequiresExclusive) > 0 && len(node.RequiresShared) > 0 {
		errs.Add("deploy %q: declares both `requires_exclusive` and `requires_shared` — a deploy claims a resource one way (sole use) or the other (shared), not both", name)
	}
	if node.Preemptible != nil {
		if shared := intersect(node.Preemptible.Holds, node.RequiresExclusive); len(shared) > 0 {
			errs.Add("deploy %q: cannot both hold and require the same exclusive token(s): %s — a holder cannot contend with itself", name, strings.Join(shared, ", "))
		}
		if shared := intersect(node.Preemptible.Holds, node.RequiresShared); len(shared) > 0 {
			errs.Add("deploy %q: cannot both hold and share the same token(s): %s — a holder cannot contend with itself", name, strings.Join(shared, ", "))
		}
	}
}

// validatePreemptibleUnified validates preemptible/requires_exclusive across a
// unified project's deploy map (which includes folded kind:check beds),
// returning the first batch of errors for the LoadUnified hard-fail path.
func validatePreemptibleUnified(uf *UnifiedFile) error {
	if uf == nil {
		return nil
	}
	errs := &ValidationError{}
	for name, node := range uf.Bundle {
		ValidatePreemptibleOnNode(name, &node, errs)
	}
	validateResourceDefs(uf, errs)
	if errs.HasErrors() {
		return fmt.Errorf("preemptible / requires_exclusive validation:\n  %s", errs.Error())
	}
	return nil
}

// validateResourceDefs checks the embedded `resource:` vocabulary
// (charly/charly.yml) and its interaction with VM-targeted claimants:
//
//   - a `gpu:` selector MUST carry a non-empty vendor (auto-allocation matches
//     DetectVFIO's reported PCI vendor against it);
//   - a `target: vm` claimant requiring a GPU resource needs `backend: libvirt`
//     on its VM entity — a PCI <hostdev> does not render under the qemu backend,
//     so auto-allocation would silently fail at create time.
func validateResourceDefs(uf *UnifiedFile, errs *ValidationError) {
	// resource is a plugin kind now (candy/plugin-resource); decode the name-keyed vocab once.
	resources := uf.resolveResources()
	for name, rdef := range resources {
		if rdef == nil {
			continue
		}
		if rdef.Gpu != nil && strings.TrimSpace(rdef.Gpu.Vendor) == "" {
			errs.Add("resource %q: `gpu.vendor` is required (e.g. \"0x10de\" for NVIDIA) — it is the PCI vendor auto-allocation matches", name)
		}
	}
	if len(resources) == 0 {
		return
	}
	for name, node := range uf.Bundle {
		if node.Target != "vm" {
			continue
		}
		if _, _, ok := requiredGPUResource(&node, resources); !ok {
			continue
		}
		vmName := node.From
		if vmName == "" {
			base, _ := parseDeployKey(name)
			vmName = base
		}
		if spec, _ := resolveVmViaPlugin(uf.VM[vmName]); spec != nil && spec.Backend == "qemu" {
			errs.Add("deploy %q requires an auto-allocated GPU but its VM %q pins `backend: qemu` — GPU passthrough needs `backend: libvirt` (PCI <hostdev> does not render under qemu)", name, vmName)
		}
	}
}
