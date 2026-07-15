package vm

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMissingDestroyPolicy(t *testing.T) {
	if err := missingDestroyError("charly-typo", false); err == nil {
		t.Fatal("strict operator destroy accepted a missing VM")
	}
	if err := missingDestroyError("charly-reconciled", true); err != nil {
		t.Fatalf("--if-exists rejected an already-absent VM: %v", err)
	}
}

// TestDestroyVmDomain_NotFound proves the #69 regression fix: a name with NO libvirt domain AND NO
// qemu state dir reports torn=false with no error (which VmDestroyCmd.Run turns into a hard "no such
// VM" non-zero exit) — never a false "Destroyed VM" success. Runs under the REAL HOME on purpose: a
// temp HOME would make the libvirt probe autospawn the session daemon rooted at a dir that vanishes
// when the test ends, breaking every later libvirt test in the process (a real isolation trap). The
// name is one no domain/state dir can match.
func TestDestroyVmDomain_NotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("probes the libvirt session daemon; skipped under -short")
	}
	torn, err := destroyVmDomain("charly-vm69-regression-nonexistent-do-not-create", false)
	if err != nil {
		t.Fatalf("destroyVmDomain(nonexistent): unexpected error: %v", err)
	}
	if torn {
		t.Fatalf("destroyVmDomain(nonexistent): torn=true — a missing VM must report NOT torn (the #69 false-success class)")
	}
}

// TestDestroyVmDomain_QemuStateDir proves the qemu arm tears the VM down and reports torn=true,
// removing the per-VM state dir. No running qemu process is needed — force-shutdown falls through to
// the state-dir removal. The libvirt probe misses the random name and falls to the qemu path. Seeds a
// uniquely-named dir under the REAL vm state dir (not a temp HOME — see TestDestroyVmDomain_NotFound)
// and cleans it up.
func TestDestroyVmDomain_QemuStateDir(t *testing.T) {
	if testing.Short() {
		t.Skip("probes the libvirt session daemon; skipped under -short")
	}
	dir, err := vmDir()
	if err != nil {
		t.Fatalf("vmDir: %v", err)
	}
	name := "charly-vm69-regression-qemu-fixture"
	stateDir := filepath.Join(dir, name)
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("seed state dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(stateDir) })

	torn, err := destroyVmDomain(name, false)
	if err != nil {
		t.Fatalf("destroyVmDomain(qemu): unexpected error: %v", err)
	}
	if !torn {
		t.Fatalf("destroyVmDomain(qemu): torn=false — a present qemu state dir must be torn down")
	}
	if _, statErr := os.Stat(stateDir); !os.IsNotExist(statErr) {
		t.Fatalf("destroyVmDomain(qemu): state dir %s still present after teardown", stateDir)
	}
}
