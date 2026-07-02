package main

import (
	"testing"

	libvirt "github.com/digitalocean/go-libvirt"
)

// TestGracefulStopDomain_ForcesActiveDomainToShutoff is the R10 for the charly vm teardown
// fix (vm_libvirt.go gracefulStopDomain): a domain in a NON-shutoff active state MUST be
// driven to SHUTOFF before the caller (VmDestroyCmd) undefines it. Before the fix,
// gracefulStopDomain early-returned on `state != domainStateRunning` (so a paused/blocked
// domain got no action) and its running-path poll returned on any transient "not running"
// state WITHOUT forcing — so `charly vm destroy` reported success while the domain kept
// running, and the undefine then made it a lingering transient (the orphaned-VM incident).
//
// This creates a real transient libvirt domain, SUSPENDS it to PAUSED (a non-running
// active state the old code no-op'd on), runs gracefulStopDomain, and asserts SHUTOFF.
// Gated behind -short (needs qemu:///session + /dev/kvm).
func TestGracefulStopDomain_ForcesActiveDomainToShutoff(t *testing.T) {
	if testing.Short() {
		t.Skip("creates a real libvirt domain (needs qemu:///session + /dev/kvm)")
	}
	conn, err := connectLibvirt("")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close() //nolint:errcheck

	const name = "charly-test-graceful-stop"
	const xml = `<domain type='kvm'>
  <name>` + name + `</name>
  <memory unit='MiB'>128</memory>
  <vcpu>1</vcpu>
  <os><type arch='x86_64' machine='q35'>hvm</type></os>
  <devices><emulator>/usr/bin/qemu-system-x86_64</emulator></devices>
</domain>`

	// Clean any leftover from a prior run.
	if dom, e := conn.lookupDomain(name); e == nil {
		_ = conn.destroyDomain(dom)
		_ = conn.undefineDomain(dom, true)
	}
	if err := conn.defineAndStartDomain(xml); err != nil {
		t.Fatalf("define+start minimal domain: %v", err)
	}
	// Always remove the definition, even on failure.
	defer func() {
		if d, e := conn.lookupDomain(name); e == nil {
			_ = conn.destroyDomain(d)
			_ = conn.undefineDomain(d, true)
		}
	}()

	dom, err := conn.lookupDomain(name)
	if err != nil {
		t.Fatalf("lookup after start: %v", err)
	}

	// Move it to a non-shutoff, non-running ACTIVE state (PAUSED) — the case the old
	// gracefulStopDomain skipped, leaving it active for the undefine to orphan.
	if err := conn.l.DomainSuspend(dom); err != nil {
		t.Fatalf("suspend: %v", err)
	}
	if st, _ := conn.domainState(dom); st == libvirt.DomainShutoff {
		t.Fatalf("domain unexpectedly SHUTOFF right after suspend")
	}

	// The fix under test must drive it to SHUTOFF.
	conn.gracefulStopDomain(dom)

	st, serr := conn.domainState(dom)
	if serr != nil {
		t.Fatalf("domainState after gracefulStopDomain: %v", serr)
	}
	if st != libvirt.DomainShutoff {
		t.Fatalf("after gracefulStopDomain a paused domain is state=%d, want SHUTOFF(%d) — the fix did not force it off", st, libvirt.DomainShutoff)
	}
}
