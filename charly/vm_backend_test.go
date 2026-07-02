package main

import (
	"strings"
	"testing"
)

// stubNoLibvirtSpawn replaces startLibvirtUserSession with a no-op for the
// duration of the test. resolveVmBackend spawns the session daemon before
// probing its socket (so on-demand-autospawn hosts are detected correctly);
// an un-stubbed real spawn would create a socket inside the test's temp
// XDG_RUNTIME_DIR and defeat the "no socket present" fixture below.
func stubNoLibvirtSpawn(t *testing.T) {
	t.Helper()
	orig := startLibvirtUserSession
	startLibvirtUserSession = func() {}
	t.Cleanup(func() { startLibvirtUserSession = orig })
}

// TestResolveVmBackend_ExplicitLibvirtMissingSocket asserts that when
// the operator explicitly requests `backend: libvirt` and no libvirt
// session daemon socket is available, resolveVmBackend returns a
// loud, actionable error — instead of silently falling back to qemu
// (which would cause every `charly check libvirt …` probe to fail with a
// confusing "no such file or directory" error 5+ minutes into the
// dispatcher run; see plan-please-use-the-plan-atomic-comet H1).
func TestResolveVmBackend_ExplicitLibvirtMissingSocket(t *testing.T) {
	// Point XDG_RUNTIME_DIR at a temp dir that has no libvirt sockets.
	// libvirtSessionSocketWithProbes() reads this env var (vm_libvirt.go).
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	stubNoLibvirtSpawn(t)

	_, err := resolveVmBackend("libvirt")
	if err == nil {
		t.Fatal("expected error for explicit backend=libvirt with no socket; got nil")
	}
	msg := err.Error()
	// The error MUST mention how to fix it — operators see only the
	// last log line in some failure modes (check-live timeout). Hint
	// pointers:
	if !strings.Contains(msg, "libvirt session daemon") {
		t.Errorf("error must mention 'libvirt session daemon', got: %q", msg)
	}
	if !strings.Contains(msg, "vm.backend qemu") && !strings.Contains(msg, "virtqemud") {
		t.Errorf("error must point at remediation (vm.backend qemu OR virtqemud), got: %q", msg)
	}
}

// TestResolveVmBackend_AutoFallsThroughToQemu asserts that the existing
// silent-fallback semantics for `backend: auto` are preserved — auto
// callers (legacy default) get qemu when libvirt is unavailable. This
// is the behavior every existing project ships today; the new explicit-
// libvirt gate above is opt-in via deploy.yml.
func TestResolveVmBackend_AutoFallsThroughToQemu(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	stubNoLibvirtSpawn(t)
	backend, err := resolveVmBackend("auto")
	if err != nil {
		// On hosts without qemu installed, this returns an error too.
		// Skip rather than fail; the test only asserts the libvirt-side
		// fallback semantics, not qemu install state.
		t.Skipf("auto resolution returned %v (no qemu binary on this host); skipping", err)
	}
	if backend != "qemu" {
		t.Errorf("auto with no libvirt socket → backend = %q, want qemu", backend)
	}
}

// TestResolveVmBackend_ExplicitQemuShortCircuits asserts the qemu
// branch doesn't even probe libvirt — operators on libvirt-less hosts
// (CI runners, containers) get a clean qemu path.
func TestResolveVmBackend_ExplicitQemuShortCircuits(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	backend, err := resolveVmBackend("qemu")
	if err != nil {
		t.Skipf("qemu resolution returned %v (no qemu binary on this host); skipping", err)
	}
	if backend != "qemu" {
		t.Errorf("explicit qemu → backend = %q, want qemu", backend)
	}
}
