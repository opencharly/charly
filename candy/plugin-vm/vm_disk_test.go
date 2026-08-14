package vm

// Relocated from charly/vm_disk_test.go (#55 decoupling cone, Batch C):
// TestVmDiskDir_PerVM asserts vmshared.VmDiskDir directly — zero charly
// coupling.

import (
	"path/filepath"
	"testing"

	"github.com/opencharly/sdk/vmshared"
)

// TestParseDiskSizeBytes asserts the `truncate -s`-compatible size parser used to compute the
// disk build's MinFreeBytes floor. The parser must match truncate's semantics: bare K/M/G/T are
// 1024-based, the iB forms are 1024-based, and the B forms (KB/MB/GB) are 1000-based.
func TestParseDiskSizeBytes(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"536870912", 536870912},
		{"20G", 20 << 30},
		{"20GiB", 20 << 30},
		{"10240M", 10240 << 20},
		{"2T", 2 << 40},
		{"500MB", 500 * 1000 * 1000},
		{"1KB", 1000},
		{" 8G ", 8 << 30},
	}
	for _, c := range cases {
		got, err := parseDiskSizeBytes(c.in)
		if err != nil {
			t.Errorf("parseDiskSizeBytes(%q) error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("parseDiskSizeBytes(%q) = %d, want %d", c.in, got, c.want)
		}
	}

	for _, bad := range []string{"", "G", "20X", "abc", "20GX"} {
		if _, err := parseDiskSizeBytes(bad); err == nil {
			t.Errorf("parseDiskSizeBytes(%q) = nil error, want an error", bad)
		}
	}
}

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
