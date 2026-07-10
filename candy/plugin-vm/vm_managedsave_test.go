package vm

import (
	"testing"
)

// TestUndefine_ClearsManagedSaveImage is the R10 check-coverage for the undefineDomain
// managed-save fix (vm_libvirt.go): libvirt REFUSES to undefine a domain that holds a
// managed save image ("Refusing to undefine while domain managed save image exists"),
// and the host libvirt shutdown handler managed-saves every running domain across a host
// reboot. Before the fix undefineDomain passed only DomainUndefineNvram, so a VM that was
// running when the host rebooted became unremovable by EVERY charly cleanup path:
// `charly vm destroy`, the defineAndStartDomain self-heal, `charly vm clean-orphans`, and
// the disposable-VM-bed `charly update` fresh-rebuild gate itself.
//
// This starts a minimal domain, managed-saves it (the exact state a host reboot leaves),
// asserts the managed save image is really there, then requires undefineDomain to succeed.
// Without DomainUndefineManagedSave in the flag set the undefine FAILS and this test fails.
// Gated behind -short (needs qemu:///session + /dev/kvm).
func TestUndefine_ClearsManagedSaveImage(t *testing.T) {
	if testing.Short() {
		t.Skip("creates a real libvirt domain (needs qemu:///session + /dev/kvm)")
	}
	conn, err := connectLibvirt("")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close() //nolint:errcheck

	const name = "charly-test-managedsave"
	const xml = `<domain type='kvm'>
  <name>` + name + `</name>
  <uuid>33333333-3333-3333-3333-333333333333</uuid>
  <memory unit='MiB'>128</memory>
  <vcpu>1</vcpu>
  <os><type arch='x86_64' machine='q35'>hvm</type></os>
  <devices><emulator>/usr/bin/qemu-system-x86_64</emulator></devices>
</domain>`

	cleanup := func() {
		if d, e := conn.lookupDomain(name); e == nil {
			_ = conn.destroyDomain(d)
			_ = conn.l.DomainManagedSaveRemove(d, 0)
			_ = conn.undefineDomain(d, true)
		}
	}
	cleanup()
	defer cleanup()

	dom, err := conn.l.DomainDefineXML(xml)
	if err != nil {
		t.Fatalf("define: %v", err)
	}
	if err := conn.startDomain(dom); err != nil {
		t.Fatalf("start: %v", err)
	}
	// Managed-save it: exactly what the host libvirt shutdown handler does on reboot.
	if err := conn.l.DomainManagedSave(dom, 0); err != nil {
		t.Fatalf("managed-save: %v", err)
	}
	if has, e := conn.l.DomainHasManagedSaveImage(dom, 0); e != nil || has == 0 {
		t.Fatalf("precondition: domain must hold a managed save image (has=%d err=%v)", has, e)
	}

	// The assertion under test. Pre-fix this returns:
	//   "Requested operation is not valid: Refusing to undefine while domain managed save image exists"
	if err := conn.undefineDomain(dom, true); err != nil {
		t.Fatalf("undefineDomain must clear the managed save image, got: %v", err)
	}
	if _, e := conn.lookupDomain(name); e == nil {
		t.Fatalf("domain %q should be gone after undefine", name)
	}
}
