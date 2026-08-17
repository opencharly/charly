package fleet

import (
	"testing"

	"github.com/opencharly/spec/spec"
)

// The distro is read from source.distro ONLY. A BaseUser-derived fallback used to fill it in, which
// is how a Debian-family guest came to be rendered with Arch package names — an account name is not
// a distro. This pins the absence: with BaseUser set and Distro empty, nothing distro-shaped may be
// derived. Restoring the fallback fails this test.
func TestBuildVmSyntheticBox_BaseUserIsNotADistro(t *testing.T) {
	distro := map[string]*spec.ResolvedDistro{
		"fedora": {Format: map[string]*spec.Format{"rpm": {}}},
	}
	img := buildVmSyntheticBox(
		&spec.ResolvedVm{Source: spec.VmSource{Kind: "cloud_image", BaseUser: "fedora"}},
		distro, nil)
	if img == nil {
		t.Fatal("buildVmSyntheticBox returned nil — the assertions below would pass vacuously")
	}
	if img.Pkg != "" {
		t.Errorf("BaseUser %q was inferred into a package format (img.Pkg = %q); an account name "+
			"is not a distro and source.distro is required by the vm kind's OpValidate", "fedora", img.Pkg)
	}
	if len(img.Distro) != 0 {
		t.Errorf("BaseUser was inferred into a distro chain (img.Distro = %v), want empty", img.Distro)
	}
}

// Presence control: the EXPLICIT field must still resolve, or the test above would pass simply
// because the derivation is broken for every input.
//
// BaseUser is set in BOTH tests deliberately, and it is not a contradiction: BaseUser's real job is
// to name the guest's SSH account, and ResolveCloudInitSSHUser reads it to decide which branch this
// function takes — an empty/root user goes to syntheticHostBoxFromEnvelope, which derives from the
// HOST distro and never reaches the block under test (that is why an earlier draft of these tests
// saw the host's "pac" for a fedora fixture). So the pair isolates exactly one variable: same user,
// Distro absent vs present.
func TestBuildVmSyntheticBox_ExplicitDistroStillResolves(t *testing.T) {
	distro := map[string]*spec.ResolvedDistro{
		"fedora": {Format: map[string]*spec.Format{"rpm": {}}},
	}
	img := buildVmSyntheticBox(
		&spec.ResolvedVm{Source: spec.VmSource{Kind: "cloud_image", BaseUser: "fedora", Distro: "fedora"}},
		distro, nil)
	if img == nil {
		t.Fatal("buildVmSyntheticBox returned nil")
	}
	if img.Pkg != "rpm" {
		t.Errorf("explicit distro fedora resolved img.Pkg = %q, want \"rpm\"", img.Pkg)
	}
}
