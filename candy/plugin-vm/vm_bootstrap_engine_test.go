package vm

import (
	"strings"
	"testing"

	"github.com/opencharly/sdk/vmshared"
)

// TestBaseBootstrapPackages_DebootstrapDispatch confirms that baseBootstrapPackages returns
// d.Debootstrap.BasePackages (the stage-2 chroot apt-get install set) for debootstrap-flavored
// distros. (P8b-rest: ported verbatim from charly/debootstrap_test.go.)
func TestBaseBootstrapPackages_DebootstrapDispatch(t *testing.T) {
	d := &vmshared.DistroDef{
		Debootstrap: &vmshared.DebootstrapDef{
			Suite:        "trixie",
			Mirror:       "http://deb.debian.org/debian",
			BasePackages: []string{"linux-image-amd64", "grub-efi-amd64", "openssh-server"},
		},
	}
	got := baseBootstrapPackages(d)
	if len(got) != 3 || got[0] != "linux-image-amd64" {
		t.Errorf("baseBootstrapPackages = %v, want [linux-image-amd64 grub-efi-amd64 openssh-server]", got)
	}
}

// TestBaseBootstrapPackages_PacstrapStillWorks ensures the pacstrap branch is untouched by the
// debootstrap wiring.
func TestBaseBootstrapPackages_PacstrapStillWorks(t *testing.T) {
	d := &vmshared.DistroDef{
		Pacstrap: &vmshared.PacstrapDef{
			BasePackages: []string{"base", "linux", "openssh"},
		},
	}
	got := baseBootstrapPackages(d)
	if len(got) != 3 || got[0] != "base" {
		t.Errorf("baseBootstrapPackages = %v, want [base linux openssh]", got)
	}
}

// TestBaseBootstrapPackages_NilDistro must not panic.
func TestBaseBootstrapPackages_NilDistro(t *testing.T) {
	if got := baseBootstrapPackages(nil); got != nil {
		t.Errorf("nil distro should yield nil, got %v", got)
	}
	if got := baseBootstrapPackages(&vmshared.DistroDef{}); got != nil {
		t.Errorf("empty distro should yield nil, got %v", got)
	}
}

// TestBootstrapTarPreservesFileCaps guards the file-capability fix: GNU tar's --xattrs
// default-EXCLUDES the security.* namespace on extract, which silently drops file
// capabilities (security.capability). A bootstrap rootfs that loses cap_setuid on
// /usr/bin/newuidmap (and cap_setgid on newgidmap) leaves rootless podman broken in the guest.
// This test would FAIL without the fix. (P8b-rest: ported from charly/vm_bootstrap_test.go —
// the charly.yml embedded-build-vocabulary half of that test stays in charly core, which is
// where the embedded config lives; this half covers the constant that moved here.)
func TestBootstrapTarPreservesFileCaps(t *testing.T) {
	if !strings.Contains(bootstrapRootfsExtractTar, "--xattrs-include") {
		t.Errorf("bootstrapRootfsExtractTar lacks --xattrs-include, so GNU tar drops "+
			"security.capability on extract (newuidmap loses cap_setuid → rootless "+
			"podman broken): %q", bootstrapRootfsExtractTar)
	}
}
