package main

import (
	"testing"

	"github.com/opencharly/sdk/kit"
)

// TestRunner_VmTargetName proves the host-side check verbs address a VM deployment by its per-deploy
// DOMAIN IDENTITY (the live libvirt domain charly-<domainID>), NOT the shared kind:vm entity. The
// go-libvirt shed moved ResolveVmTarget out-of-process (it can no longer LoadUnified to compute the
// domain), so the host threads the already-resolved domain identity to the spice/libvirt verbs via
// kit.Runner.VmTargetName(); without this the operator-side probes looked up the wrong name and failed
// "domain not found". Post-P33 the domain is named after the DEPLOY, so the check runner sets VmName to
// the domain identity (vmDomainIdentity of the bed/deploy key), not the entity.
func TestRunner_VmTargetName(t *testing.T) {
	// VM deployment: VmName (the per-deploy domain identity) wins, so the operator-side
	// libvirt/spice verbs address charly-<domainID> = charly-<deploy-name>.
	r := kit.NewRunner(kit.RunnerConfig{Box: "check-arch-vm", VmName: "check-arch-vm"})
	if got := r.VmTargetName(); got != "check-arch-vm" {
		t.Fatalf("VM deployment: want domain identity %q, got %q", "check-arch-vm", got)
	}
	// Pod deployment: VmName empty → fall back to Box (the container name), so a
	// cdp/wl/dbus/vnc verb still addresses charly-<deploy-name>.
	r = kit.NewRunner(kit.RunnerConfig{Box: "check-pod"})
	if got := r.VmTargetName(); got != "check-pod" {
		t.Fatalf("pod deployment: want deploy name %q, got %q", "check-pod", got)
	}
}
