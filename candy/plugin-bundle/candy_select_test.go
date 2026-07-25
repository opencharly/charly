package bundle

import (
	"reflect"
	"testing"

	"github.com/opencharly/sdk/spec"
)

// candy_select_test.go — relocated (K4 unit B, core-min wave 3) from the DELETED
// charly/synthetic_vm_image_test.go: the regression guard for the non-arch VM deploy bug moves
// with its function. buildVmSyntheticBox is the pure field-derivation half of
// syntheticVmBoxFromEnvelope split out specifically so this coverage needs no live kind:vm
// provider RPC.

// TestBuildVmSyntheticBoxDistroFormat is the regression guard for the non-arch VM deploy bug: the
// synthetic VM box used to hardcode Distro:["arch"]/Pkg:"pac"/BuildFormats:["pac"] for EVERY
// non-root VM, so a candy deploy (and the `charly` localpkg) onto a debian/ubuntu/fedora guest ran
// `pacman` and failed with exit 127. The fix derives the guest's real distro + primary package
// format from the VM spec — bootstrap `distro:` or cloud_image `base_user:` — so apt/dnf is used
// on those guests.
//
// Without the fix every row below would resolve Pkg="pac" and FAIL.
func TestBuildVmSyntheticBoxDistroFormat(t *testing.T) {
	distro := map[string]*spec.ResolvedDistro{
		"arch":    {Format: map[string]*spec.Format{"pac": {}, "aur": {Secondary: true}}},
		"cachyos": {Inherits: "arch", InheritPackages: true}, // pulls arch package sections
		"debian":  {Format: map[string]*spec.Format{"deb": {}}},
		"ubuntu":  {Inherits: "debian"}, // inherits debian's deb FORMAT, NOT its packages
		"fedora":  {Format: map[string]*spec.Format{"rpm": {}}},
	}

	cases := []struct {
		name       string
		vmSpec     *spec.ResolvedVm
		wantUser   string
		wantPkg    string
		wantDistro []string
	}{
		{
			name:       "debian debootstrap (bootstrap distro)",
			vmSpec:     &spec.ResolvedVm{Source: spec.VmSource{Kind: "bootstrap", Distro: "debian"}, SSH: &spec.VmSSH{User: "debian"}},
			wantUser:   "debian",
			wantPkg:    "deb",
			wantDistro: []string{"debian"},
		},
		{
			name:       "ubuntu debootstrap (inherits debian -> deb)",
			vmSpec:     &spec.ResolvedVm{Source: spec.VmSource{Kind: "bootstrap", Distro: "ubuntu"}, SSH: &spec.VmSSH{User: "ubuntu"}},
			wantUser:   "ubuntu",
			wantPkg:    "deb",
			wantDistro: []string{"ubuntu"},
		},
		{
			name:       "fedora cloud (base_user)",
			vmSpec:     &spec.ResolvedVm{Source: spec.VmSource{Kind: "cloud_image", BaseUser: "fedora"}},
			wantUser:   "fedora",
			wantPkg:    "rpm",
			wantDistro: []string{"fedora"},
		},
		{
			name:       "arch cloud (base_user)",
			vmSpec:     &spec.ResolvedVm{Source: spec.VmSource{Kind: "cloud_image", BaseUser: "arch"}},
			wantUser:   "arch",
			wantPkg:    "pac",
			wantDistro: []string{"arch"},
		},
		{
			// cachyos sets inherit_packages: true, so its VM distro chain expands
			// to [cachyos, arch] — an `arch:` candy block reaches the cachyos VM.
			// Pkg is still the resolved pac primary (aur is secondary, skipped).
			name:       "cachyos bootstrap (inherit_packages -> [cachyos, arch], pac primary)",
			vmSpec:     &spec.ResolvedVm{Source: spec.VmSource{Kind: "bootstrap", Distro: "cachyos"}, SSH: &spec.VmSSH{User: "cachyos"}},
			wantUser:   "cachyos",
			wantPkg:    "pac",
			wantDistro: []string{"cachyos", "arch"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			img := buildVmSyntheticBox(tc.vmSpec, distro)
			if img.User != tc.wantUser {
				t.Errorf("User = %q, want %q", img.User, tc.wantUser)
			}
			if img.UID != 1000 || img.GID != 1000 {
				t.Errorf("UID/GID = %d/%d, want 1000/1000", img.UID, img.GID)
			}
			if img.Home != "/home/"+tc.wantUser {
				t.Errorf("Home = %q, want %q", img.Home, "/home/"+tc.wantUser)
			}
			if img.Pkg != tc.wantPkg {
				t.Errorf("Pkg = %q, want %q (the non-arch deploy bug forced pac)", img.Pkg, tc.wantPkg)
			}
			if len(img.BuildFormats) != 1 || img.BuildFormats[0] != tc.wantPkg {
				t.Errorf("BuildFormats = %v, want [%q]", img.BuildFormats, tc.wantPkg)
			}
			if !reflect.DeepEqual(img.Distro, tc.wantDistro) {
				t.Errorf("Distro = %v, want %v (inherits chain must be appended)", img.Distro, tc.wantDistro)
			}
		})
	}
}

// TestBuildVmSyntheticBoxRootFallback: a bootc VM with no SSH user resolves to the root branch
// (System scope, /root home), unchanged by the distro fix.
func TestBuildVmSyntheticBoxRootFallback(t *testing.T) {
	distro := map[string]*spec.ResolvedDistro{
		"fedora": {Format: map[string]*spec.Format{"rpm": {}}},
	}
	img := buildVmSyntheticBox(&spec.ResolvedVm{Source: spec.VmSource{Kind: "bootc"}}, distro)
	if img.User != "root" {
		t.Errorf("User = %q, want root", img.User)
	}
	if img.Home != "/root" {
		t.Errorf("Home = %q, want /root", img.Home)
	}
}
