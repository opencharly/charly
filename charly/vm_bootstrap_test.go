package main

import (
	"os"
	"strings"
	"testing"
)

// TestBootstrapTarPreservesFileCaps guards the file-capability fix: GNU tar's
// --xattrs default-EXCLUDES the security.* namespace on extract, which silently
// drops file capabilities (security.capability). A bootstrap rootfs that loses
// the cap_setuid on /usr/bin/newuidmap (and cap_setgid on newgidmap) leaves
// rootless podman broken in the guest — the exact failure that hung the nested
// pod-in-VM deploy (host `podman save | ssh podman load` stalled because the
// guest's rootless `podman load` could not map namespaces). Every bootstrap tar
// that round-trips a rootfs MUST carry --xattrs-include so security.* survives.
//
// This test would FAIL without the fix (the flag absent on extract/create).
func TestBootstrapTarPreservesFileCaps(t *testing.T) {
	// 1. The Go extract command (vm_bootstrap.go) — the confirmed culprit.
	if !strings.Contains(bootstrapRootfsExtractTar, "--xattrs-include") {
		t.Errorf("bootstrapRootfsExtractTar lacks --xattrs-include, so GNU tar drops "+
			"security.capability on extract (newuidmap loses cap_setuid → rootless "+
			"podman broken): %q", bootstrapRootfsExtractTar)
	}

	// 2. Every `tar … --xattrs` line in the bootstrap builders must also carry
	//    --xattrs-include (create side; defensive + symmetric with extract).
	//    A generic scan so any future bootstrap tar is caught too.
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
