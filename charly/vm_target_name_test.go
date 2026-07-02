package main

import "testing"

// TestRunner_vmTargetName proves FU-8: the host-side check verbs address a VM
// deployment by its resolved vm: ENTITY name, not the deploy/bed name. The
// go-libvirt shed moved ResolveVmTarget out-of-process (it can no longer
// LoadUnified to remap deploy→entity), so the host must thread the already-resolved
// entity name to the spice/libvirt verbs via vmTargetName(); without this the
// operator-side probes looked up charly-<deploy-name> and failed "domain not found".
func TestRunner_vmTargetName(t *testing.T) {
	// VM deployment: VmName (the resolved vm: entity) wins, so the operator-side
	// libvirt/spice verbs address charly-<vm-entity>, not charly-<deploy-name>.
	r := &Runner{Box: "check-arch-vm", VmName: "arch"}
	if got := r.vmTargetName(); got != "arch" {
		t.Fatalf("VM deployment: want vm-entity %q, got %q", "arch", got)
	}
	// Pod deployment: VmName empty → fall back to Box (the container name), so a
	// cdp/wl/dbus/vnc verb still addresses charly-<deploy-name>.
	r = &Runner{Box: "check-pod"}
	if got := r.vmTargetName(); got != "check-pod" {
		t.Fatalf("pod deployment: want deploy name %q, got %q", "check-pod", got)
	}
}
