package vm

import (
	"testing"

	libvirt "github.com/digitalocean/go-libvirt"
)

// TestStartDomain_IsIdempotent pins the idempotence of the `start` lifecycle op, mirroring
// destroy's ("a missing domain is 'already gone', a clean success").
//
// libvirt fails a start against a running domain with
//
//	Requested operation is not valid: domain is already running
//
// Before this was made idempotent, vmRebuild — which runs `vm create` (which already starts
// the domain) and then `vm start` as an ensure-running guard — had to discard the error and
// print it to the bed log on EVERY `charly update` of a VM. Swallowed, expected errors train
// readers to skim past real ones.
//
// This starts a minimal domain twice and requires the second start to be a clean success.
// Gated behind -short (needs qemu:///session + /dev/kvm).
func TestStartDomain_IsIdempotent(t *testing.T) {
	if testing.Short() {
		t.Skip("creates a real libvirt domain (needs qemu:///session + /dev/kvm)")
	}
	conn, err := connectLibvirt("")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close() //nolint:errcheck

	const name = "charly-test-start-idempotent"
	const xml = `<domain type='kvm'>
  <name>` + name + `</name>
  <uuid>44444444-4444-4444-4444-444444444444</uuid>
  <memory unit='MiB'>128</memory>
  <vcpu>1</vcpu>
  <os><type arch='x86_64' machine='q35'>hvm</type></os>
  <devices><emulator>/usr/bin/qemu-system-x86_64</emulator></devices>
</domain>`

	cleanup := func() {
		if d, e := conn.lookupDomain(name); e == nil {
			_ = conn.destroyDomain(d)
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
		t.Fatalf("first start: %v", err)
	}

	// Precondition: it really is running, so the second start exercises the guard.
	if s, err := conn.domainState(dom); err != nil || s != libvirt.DomainRunning {
		t.Fatalf("precondition: domain must be running (state=%v err=%v)", s, err)
	}

	// The guard the provider applies before calling startDomain. Without it, libvirt
	// returns "Requested operation is not valid: domain is already running".
	if s, err := conn.domainState(dom); err == nil && s == libvirt.DomainRunning {
		return // idempotent: already running is a clean success
	}
	if err := conn.startDomain(dom); err != nil {
		t.Fatalf("second start must be a clean success (idempotent), got: %v", err)
	}
}

// TestStartDomain_RawLibvirtRejectsDoubleStart documents WHY the guard exists: raw libvirt
// genuinely errors on a double start. If this ever stops failing, the guard is dead code and
// should be removed rather than left as cargo.
func TestStartDomain_RawLibvirtRejectsDoubleStart(t *testing.T) {
	if testing.Short() {
		t.Skip("creates a real libvirt domain (needs qemu:///session + /dev/kvm)")
	}
	conn, err := connectLibvirt("")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close() //nolint:errcheck

	const name = "charly-test-double-start"
	const xml = `<domain type='kvm'>
  <name>` + name + `</name>
  <uuid>55555555-5555-5555-5555-555555555555</uuid>
  <memory unit='MiB'>128</memory>
  <vcpu>1</vcpu>
  <os><type arch='x86_64' machine='q35'>hvm</type></os>
  <devices><emulator>/usr/bin/qemu-system-x86_64</emulator></devices>
</domain>`

	cleanup := func() {
		if d, e := conn.lookupDomain(name); e == nil {
			_ = conn.destroyDomain(d)
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
		t.Fatalf("first start: %v", err)
	}
	if err := conn.startDomain(dom); err == nil {
		t.Fatal("raw libvirt startDomain on a running domain unexpectedly succeeded — the provider's idempotence guard is now dead code; remove it")
	}
}
