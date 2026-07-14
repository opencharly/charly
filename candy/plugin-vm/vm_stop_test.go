package vm

import (
	"os"
	"path/filepath"
	"testing"
)

// TestStopVmDomain_NotFound proves the #77 regression fix: a name with NO libvirt domain AND NO qemu
// state dir reports stopped=false with no error (which stopVM turns into a hard "no such VM" non-zero
// exit) — never a false "Stopped VM" success. The stop sibling of #69's destroy false-success, sharing
// the vmHolder probe. Runs under the REAL HOME on purpose: a temp HOME would make the libvirt probe
// autospawn the session daemon rooted at a dir that vanishes when the test ends, breaking every later
// libvirt test in the process (a real isolation trap — see TestDestroyVmDomain_NotFound). The name is
// one no domain/state dir can match.
func TestStopVmDomain_NotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("probes the libvirt session daemon; skipped under -short")
	}
	stopped, err := stopVmDomain("charly-vm77-regression-nonexistent-do-not-create", false)
	if err != nil {
		t.Fatalf("stopVmDomain(nonexistent): unexpected error: %v", err)
	}
	if stopped {
		t.Fatalf("stopVmDomain(nonexistent): stopped=true — a missing VM must report NOT stopped (the #77 false-success class)")
	}
}

// TestStopVmDomain_QemuStateDir proves the qemu arm reports stopped=true while PRESERVING the per-VM
// state dir — the "stopped, not depleted" semantic (disk + definition kept, unlike destroy's removal).
// No running qemu process is needed: the graceful QMP shutdown fails to find a QMP socket and falls
// through to the PID-file SIGTERM fallback (a no-op with no pidfile), so stopVmDomain reports the
// domain found-and-stopped without touching the dir. The libvirt probe misses the random name and
// falls to the qemu path. Seeds a uniquely-named dir under the REAL vm state dir (not a temp HOME —
// see TestStopVmDomain_NotFound) and cleans it up.
func TestStopVmDomain_QemuStateDir(t *testing.T) {
	if testing.Short() {
		t.Skip("probes the libvirt session daemon; skipped under -short")
	}
	dir, err := vmDir()
	if err != nil {
		t.Fatalf("vmDir: %v", err)
	}
	name := "charly-vm77-regression-qemu-fixture"
	stateDir := filepath.Join(dir, name)
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("seed state dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(stateDir) })

	stopped, err := stopVmDomain(name, false)
	if err != nil {
		t.Fatalf("stopVmDomain(qemu): unexpected error: %v", err)
	}
	if !stopped {
		t.Fatalf("stopVmDomain(qemu): stopped=false — a present qemu state dir must be found and stopped")
	}
	if _, statErr := os.Stat(stateDir); statErr != nil {
		t.Fatalf("stopVmDomain(qemu): state dir %s MUST still exist after a stop (stop preserves the disk + definition, unlike destroy): %v", stateDir, statErr)
	}
}
