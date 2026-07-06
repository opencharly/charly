package vm

import (
	"testing"
)

// TestDefineAndStart_ReconcilesDriftedUUIDLeftover is the R10 for the vm-create
// robustness fix (vm_libvirt.go defineAndStartDomain): a leftover domain of the SAME
// NAME but a DIFFERENT (drifted) UUID — what a crashed or force-left disposable run
// leaves behind — must be reconciled (undefined by name) before DomainDefineXML, which
// otherwise refuses with "domain X already exists with uuid Y". Before the fix, a
// disposable VM bed's `charly update` re-run collided on exactly this and never
// self-healed (the check-k3s-vm incident during Cutover L).
//
// This seeds a stale same-name domain with UUID A, then calls defineAndStartDomain with
// UUID B and asserts success. Without the reconcile the define collides and this FAILS.
// Gated behind -short (needs qemu:///session + /dev/kvm).
func TestDefineAndStart_ReconcilesDriftedUUIDLeftover(t *testing.T) {
	if testing.Short() {
		t.Skip("creates a real libvirt domain (needs qemu:///session + /dev/kvm)")
	}
	conn, err := connectLibvirt("")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close() //nolint:errcheck

	const name = "charly-test-reconcile"
	xml := func(uuid string) string {
		return `<domain type='kvm'>
  <name>` + name + `</name>
  <uuid>` + uuid + `</uuid>
  <memory unit='MiB'>128</memory>
  <vcpu>1</vcpu>
  <os><type arch='x86_64' machine='q35'>hvm</type></os>
  <devices><emulator>/usr/bin/qemu-system-x86_64</emulator></devices>
</domain>`
	}
	cleanup := func() {
		if d, e := conn.lookupDomain(name); e == nil {
			_ = conn.destroyDomain(d)
			_ = conn.undefineDomain(d, true)
		}
	}
	cleanup()
	defer cleanup()

	const uuidA = "11111111-1111-1111-1111-111111111111"
	const uuidB = "22222222-2222-2222-2222-222222222222"

	// Seed the stale same-name leftover with UUID A (defined; the drift scenario).
	if _, e := conn.l.DomainDefineXML(xml(uuidA)); e != nil {
		t.Fatalf("seed stale domain (uuid A): %v", e)
	}
	// Create with a DIFFERENT UUID B — must reconcile the leftover, not collide.
	if err := conn.defineAndStartDomain(xml(uuidB), name); err != nil {
		t.Fatalf("defineAndStartDomain must reconcile the drifted-UUID leftover, got: %v", err)
	}
	if _, e := conn.lookupDomain(name); e != nil {
		t.Fatalf("domain %q should exist after reconcile+create: %v", name, e)
	}
}
