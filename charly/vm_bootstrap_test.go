package main

import (
	"os"
	"strings"
	"testing"
)

// TestEmbeddedBootstrapTarsPreserveFileCaps guards the file-capability fix: GNU tar's
// --xattrs default-EXCLUDES the security.* namespace on extract, which silently
// drops file capabilities (security.capability). A bootstrap rootfs that loses
// the cap_setuid on /usr/bin/newuidmap (and cap_setgid on newgidmap) leaves
// rootless podman broken in the guest — the exact failure that hung the nested
// pod-in-VM deploy (host `podman save | ssh podman load` stalled because the
// guest's rootless `podman load` could not map namespaces). Every bootstrap tar
// that round-trips a rootfs MUST carry --xattrs-include so security.* survives.
//
// The Go extract command's OWN check (bootstrapRootfsExtractTar) moved to
// candy/plugin-vm/vm_bootstrap_engine_test.go (P8b-rest: the VM-bootstrap disk-build
// engine moved into the plugin). This test keeps the create-side scan, which is
// intrinsically core-side — it reads the embedded default build vocabulary
// (charly/charly.yml, living in this same package dir), not anything that moved.
func TestEmbeddedBootstrapTarsPreserveFileCaps(t *testing.T) {
	// Every `tar … --xattrs` line in the bootstrap builders must also carry
	// --xattrs-include (create side; defensive + symmetric with the plugin's
	// extract-side check). A generic scan so any future bootstrap tar is caught too.
	// charly.yml is the binary's embedded default config (build vocabulary +
	// sidecar templates), living in the charly/ package dir (same dir as this
	// test), not the repo root. The tar lines live in its builder install
	// templates (""" strings), scannable as plain text.
	data, err := os.ReadFile("charly.yml")
	if err != nil {
		t.Fatalf("reading charly.yml: %v", err)
	}
	for i, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, "tar ") && strings.Contains(line, "--xattrs") &&
			!strings.Contains(line, "--xattrs-include") {
			t.Errorf("charly.yml line %d: `tar --xattrs` without --xattrs-include drops "+
				"file capabilities (security.capability): %s", i+1, strings.TrimSpace(line))
		}
	}
}
