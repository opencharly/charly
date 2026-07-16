package main

import (
	"github.com/opencharly/sdk/spec"
	"reflect"
	"testing"
)

// TestSyntheticVmImageDistroFormat is the regression guard for the
// non-arch VM deploy bug: syntheticVmBox used to hardcode
// Distro:["arch"]/Pkg:"pac"/BuildFormats:["pac"] for EVERY non-root VM, so
// a candy deploy (and the `charly` localpkg) onto a debian/ubuntu/fedora guest
// ran `pacman` and failed with exit 127. The fix derives the guest's real
// distro + primary package format from the VM spec — bootstrap `distro:` or
// cloud_image `base_user:` — so apt/dnf is used on those guests.
//
// Without the fix every row below would resolve Pkg="pac" and FAIL.
func TestSyntheticVmImageDistroFormat(t *testing.T) {
	distroCfg := &DistroConfig{Distro: map[string]*spec.ResolvedDistro{
		"arch":    {Format: map[string]*FormatDef{"pac": {}, "aur": {Secondary: true}}},
		"cachyos": {Inherits: "arch", InheritPackages: true}, // pulls arch package sections
		"debian":  {Format: map[string]*FormatDef{"deb": {}}},
		"ubuntu":  {Inherits: "debian"}, // inherits debian's deb FORMAT, NOT its packages
		"fedora":  {Format: map[string]*FormatDef{"rpm": {}}},
	}}

	cases := []struct {
		name       string
		spec       *VmSpec
		wantUser   string
		wantPkg    string
		wantDistro []string
	}{
		{
			name:       "debian debootstrap (bootstrap distro)",
			spec:       &VmSpec{Source: VmSource{Kind: "bootstrap", Distro: "debian"}, SSH: &VmSSH{User: "debian"}},
			wantUser:   "debian",
			wantPkg:    "deb",
			wantDistro: []string{"debian"},
		},
		{
			name:       "ubuntu debootstrap (inherits debian -> deb)",
			spec:       &VmSpec{Source: VmSource{Kind: "bootstrap", Distro: "ubuntu"}, SSH: &VmSSH{User: "ubuntu"}},
			wantUser:   "ubuntu",
			wantPkg:    "deb",
			wantDistro: []string{"ubuntu"},
		},
		{
			name:       "fedora cloud (base_user)",
			spec:       &VmSpec{Source: VmSource{Kind: "cloud_image", BaseUser: "fedora"}},
			wantUser:   "fedora",
			wantPkg:    "rpm",
			wantDistro: []string{"fedora"},
		},
		{
			name:       "arch cloud (base_user)",
			spec:       &VmSpec{Source: VmSource{Kind: "cloud_image", BaseUser: "arch"}},
			wantUser:   "arch",
			wantPkg:    "pac",
			wantDistro: []string{"arch"},
		},
		{
			// cachyos sets inherit_packages: true, so its VM distro chain expands
			// to [cachyos, arch] — an `arch:` candy block reaches the cachyos VM.
			// Pkg is still the resolved pac primary (aur is secondary, skipped).
			name:       "cachyos bootstrap (inherit_packages -> [cachyos, arch], pac primary)",
			spec:       &VmSpec{Source: VmSource{Kind: "bootstrap", Distro: "cachyos"}, SSH: &VmSSH{User: "cachyos"}},
			wantUser:   "cachyos",
			wantPkg:    "pac",
			wantDistro: []string{"cachyos", "arch"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			img := syntheticVmBox(tc.spec, distroCfg)
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

// TestResolveVmEntity is the regression guard for the bed-deploy reach bug:
// a kind:check bed (and any deploy.yml target:vm entry) names its VM via the
// node's `vm:` cross-ref, NOT a "vm:"-prefixed deploy name. Before the fix the
// candy compiler only recognized the "vm:" prefix, so a bed fell through to
// syntheticHostBox (host distro → pac) and the deploy ran `pacman` on a
// debian/fedora guest. resolveVmEntity must surface node.Vm so syntheticVmBox
// is reached.
func TestResolveVmEntity(t *testing.T) {
	cases := []struct {
		name       string
		deployName string
		node       *BundleNode
		want       string
	}{
		{"bed via node.vm (the bug)", "check-fedora-vm", &BundleNode{From: "fedora-vm"}, "fedora-vm"},
		{"deploy.yml target:vm via node.vm", "my-guest", &BundleNode{Target: "vm", From: "arch"}, "arch"},
		{"cli vm: prefix, no node", "vm:arch", nil, "arch"},
		{"node.vm wins over prefix", "vm:ignored", &BundleNode{From: "real-vm"}, "real-vm"},
		{"non-vm deploy -> empty", "my-pod", &BundleNode{}, ""},
		{"nil node, non-prefixed -> empty", "some-pod", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveVmEntity(tc.deployName, tc.node); got != tc.want {
				t.Errorf("resolveVmEntity(%q, %+v) = %q, want %q", tc.deployName, tc.node, got, tc.want)
			}
		})
	}
}

// TestSyntheticVmImageRootFallback: a bootc VM with no SSH user resolves to
// the root branch (System scope, /root home), unchanged by the distro fix.
func TestSyntheticVmImageRootFallback(t *testing.T) {
	distroCfg := &DistroConfig{Distro: map[string]*spec.ResolvedDistro{
		"fedora": {Format: map[string]*FormatDef{"rpm": {}}},
	}}
	img := syntheticVmBox(&VmSpec{Source: VmSource{Kind: "bootc"}}, distroCfg)
	if img.User != "root" {
		t.Errorf("User = %q, want root", img.User)
	}
	if img.Home != "/root" {
		t.Errorf("Home = %q, want /root", img.Home)
	}
}
