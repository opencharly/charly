package vm

import (
	"encoding/json"
	"testing"

	libvirt "github.com/digitalocean/go-libvirt"
)

// seedRunningDomain defines + starts a minimal domain and returns it with a cleanup func.
//
// The caller MUST `defer cleanup()` AFTER its `defer conn.Close()`, so LIFO runs cleanup
// while the connection is still open. t.Cleanup is deliberately NOT used: it fires after the
// test function's defers, i.e. after conn.Close(), so every teardown call would run on a
// closed connection, silently fail (its error is discarded), and leak a running libvirt
// domain onto the host.
func seedRunningDomain(t *testing.T, conn *libvirtConn, name, uuid string) (libvirt.Domain, func()) {
	t.Helper()
	xml := `<domain type='kvm'>
  <name>` + name + `</name>
  <uuid>` + uuid + `</uuid>
  <memory unit='MiB'>128</memory>
  <vcpu>1</vcpu>
  <os><type arch='x86_64' machine='q35'>hvm</type></os>
  <devices><emulator>/usr/bin/qemu-system-x86_64</emulator></devices>
</domain>`
	cleanup := func() {
		d, e := conn.lookupDomain(name)
		if e != nil {
			return
		}
		if err := conn.destroyDomain(d); err != nil {
			t.Logf("cleanup: destroy %s: %v", name, err)
		}
		if err := conn.undefineDomain(d, true); err != nil {
			t.Errorf("cleanup: undefine %s leaked the domain: %v", name, err)
		}
	}
	cleanup() // clear a leftover from an interrupted earlier run

	dom, err := conn.l.DomainDefineXML(xml)
	if err != nil {
		t.Fatalf("define %s: %v", name, err)
	}
	if err := conn.startDomain(dom); err != nil {
		cleanup()
		t.Fatalf("start %s: %v", name, err)
	}
	if s, err := conn.domainState(dom); err != nil || s != libvirt.DomainRunning {
		cleanup()
		t.Fatalf("precondition: %s must be running (state=%v err=%v)", name, s, err)
	}
	return dom, cleanup
}

// TestStartOp_IsIdempotent drives the PROVIDER op — dispatchInternalOp with VmOp "start" —
// against an already-running domain, which is the code path `charly vm start` and vmRebuild
// actually take. It must report a clean success carrying already_running.
//
// This deliberately does NOT re-implement the guard: it asserts the provider's observable
// reply. Remove the DomainRunning short-circuit from provider.go and this test FAILS with
// libvirt's "Requested operation is not valid: domain is already running" surfaced in the
// reply's error field. (An earlier version of this test inlined the guard itself and passed
// with the fix removed — a check that cannot fail proves nothing.)
//
// Gated behind -short (needs qemu:///session + /dev/kvm).
func TestStartOp_IsIdempotent(t *testing.T) {
	if testing.Short() {
		t.Skip("creates a real libvirt domain (needs qemu:///session + /dev/kvm)")
	}
	conn, err := connectLibvirt("")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close() //nolint:errcheck

	const name = "charly-test-start-idempotent"
	_, cleanup := seedRunningDomain(t, conn, name, "44444444-4444-4444-4444-444444444444")
	defer cleanup() // LIFO: runs BEFORE conn.Close()

	reply, err := dispatchInternalOp(vmEnv{VmOp: "start", VmName: name})
	if err != nil {
		t.Fatalf("dispatchInternalOp(start): transport error: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(reply.GetResultJson(), &got); err != nil {
		t.Fatalf("reply is not JSON: %v (%s)", err, reply.GetResultJson())
	}
	if e, ok := got["error"].(string); ok && e != "" {
		t.Fatalf("start on a running domain must be a clean success, got error: %s", e)
	}
	if ok, _ := got["ok"].(bool); !ok {
		t.Fatalf("start on a running domain must report ok:true, got: %v", got)
	}
	if ar, _ := got["already_running"].(bool); !ar {
		t.Fatalf("start on a running domain must report already_running:true, got: %v", got)
	}
}

// TestStartOp_StartsAStoppedDomain is the other half: idempotence must not mean "never start".
// A defined-but-shut-off domain must still be started, and must NOT report already_running.
func TestStartOp_StartsAStoppedDomain(t *testing.T) {
	if testing.Short() {
		t.Skip("creates a real libvirt domain (needs qemu:///session + /dev/kvm)")
	}
	conn, err := connectLibvirt("")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close() //nolint:errcheck

	const name = "charly-test-start-stopped"
	dom, cleanup := seedRunningDomain(t, conn, name, "66666666-6666-6666-6666-666666666666")
	defer cleanup() // LIFO: runs BEFORE conn.Close()

	// Stop it, so the op has real work to do.
	if err := conn.destroyDomain(dom); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if s, err := conn.domainState(dom); err != nil || s == libvirt.DomainRunning {
		t.Fatalf("precondition: domain must be off (state=%v err=%v)", s, err)
	}

	reply, err := dispatchInternalOp(vmEnv{VmOp: "start", VmName: name})
	if err != nil {
		t.Fatalf("dispatchInternalOp(start): %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(reply.GetResultJson(), &got); err != nil {
		t.Fatalf("reply is not JSON: %v", err)
	}
	if e, ok := got["error"].(string); ok && e != "" {
		t.Fatalf("start on a stopped domain must succeed, got error: %s", e)
	}
	if ar, _ := got["already_running"].(bool); ar {
		t.Fatal("start on a STOPPED domain must not report already_running")
	}
	if s, err := conn.domainState(dom); err != nil || s != libvirt.DomainRunning {
		t.Fatalf("domain must be running after start (state=%v err=%v)", s, err)
	}
}

// TestRawLibvirtRejectsDoubleStart documents WHY the guard exists: raw libvirt genuinely errors
// on a double start. If this ever stops failing, the guard is dead code and should be removed
// rather than left as cargo.
func TestRawLibvirtRejectsDoubleStart(t *testing.T) {
	if testing.Short() {
		t.Skip("creates a real libvirt domain (needs qemu:///session + /dev/kvm)")
	}
	conn, err := connectLibvirt("")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close() //nolint:errcheck

	const name = "charly-test-double-start"
	dom, cleanup := seedRunningDomain(t, conn, name, "55555555-5555-5555-5555-555555555555")
	defer cleanup() // LIFO: runs BEFORE conn.Close()

	if err := conn.startDomain(dom); err == nil {
		t.Fatal("raw libvirt startDomain on a running domain unexpectedly succeeded — the provider's idempotence guard is now dead code; remove it")
	}
}
