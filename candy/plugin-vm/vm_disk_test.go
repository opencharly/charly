package vm

// Relocated from charly/vm_disk_test.go (#55 decoupling cone, Batch C):
// TestVmDiskDir_PerVM asserts vmshared.VmDiskDir directly — zero charly
// coupling.

import (
	"path/filepath"
	"testing"

	"github.com/opencharly/sdk/vmshared"
)

// TestVmDiskDir_PerVM asserts disk/seed output is namespaced per VM, so building
// or creating one VM never reuses a sibling VM's disk or (critically) its stale
// seed.iso — the regression that made `charly vm create cachyos-gpu` adopt the
// bed VM's seed (whose embedded SSH key mismatched cachyos-gpu's id_ed25519).
func TestVmDiskDir_PerVM(t *testing.T) {
	coder := vmshared.VmDiskDir("cachyos-gpu")
	bed := vmshared.VmDiskDir("cachyos-gpu-vm")
	if coder == bed {
		t.Fatalf("vmshared.VmDiskDir must be per-VM; got identical paths for two VMs: %s", coder)
	}
	want := filepath.Join("output", "qcow2", "cachyos-gpu")
	if coder != want {
		t.Errorf("vmshared.VmDiskDir(cachyos-gpu) = %q, want %q", coder, want)
	}
}
