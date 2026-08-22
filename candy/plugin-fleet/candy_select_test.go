package fleet

import (
	"reflect"
	"testing"

	"github.com/opencharly/spec/spec"
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
			vmSpec:     &spec.ResolvedVm{Source: spec.VmSource{Kind: "bootstrap", Distro: "debian"}, SSH: &spec.VmSsh{User: "debian"}},
			wantUser:   "debian",
			wantPkg:    "deb",
			wantDistro: []string{"debian"},
		},
		{
			name:       "ubuntu debootstrap (inherits debian -> deb)",
			vmSpec:     &spec.ResolvedVm{Source: spec.VmSource{Kind: "bootstrap", Distro: "ubuntu"}, SSH: &spec.VmSsh{User: "ubuntu"}},
			wantUser:   "ubuntu",
			wantPkg:    "deb",
			wantDistro: []string{"ubuntu"},
		},
		{
			name:       "fedora cloud (base_user)",
			vmSpec:     &spec.ResolvedVm{Source: spec.VmSource{Kind: "cloud_image", BaseUser: "fedora", Distro: "fedora"}},
			wantUser:   "fedora",
			wantPkg:    "rpm",
			wantDistro: []string{"fedora"},
		},
		{
			name:       "arch cloud (base_user)",
			vmSpec:     &spec.ResolvedVm{Source: spec.VmSource{Kind: "cloud_image", BaseUser: "arch", Distro: "arch"}},
			wantUser:   "arch",
			wantPkg:    "pac",
			wantDistro: []string{"arch"},
		},
		{
			// cachyos sets inherit_packages: true, so its VM distro chain expands
			// to [cachyos, arch] — an `arch:` candy block reaches the cachyos VM.
			// Pkg is still the resolved pac primary (aur is secondary, skipped).
			name:       "cachyos bootstrap (inherit_packages -> [cachyos, arch], pac primary)",
			vmSpec:     &spec.ResolvedVm{Source: spec.VmSource{Kind: "bootstrap", Distro: "cachyos"}, SSH: &spec.VmSsh{User: "cachyos"}},
			wantUser:   "cachyos",
			wantPkg:    "pac",
			wantDistro: []string{"cachyos", "arch"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			img := buildVmSyntheticBox(tc.vmSpec, distro, nil)
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
	img := buildVmSyntheticBox(&spec.ResolvedVm{Source: spec.VmSource{Kind: "bootc"}}, distro, nil)
	if img.User != "root" {
		t.Errorf("User = %q, want root", img.User)
	}
	if img.Home != "/root" {
		t.Errorf("Home = %q, want /root", img.Home)
	}
}

// TestSyntheticHostBoxFromEnvelope_SetsBuilderConfig is the regression guard for #118 task #56:
// a synthetic box with a nil BuilderConfig made compileBuilderSteps early-return zero steps,
// silently skipping the npm/pixi/cargo/aur builder DEPLOY leg for EVERY standalone-candy deploy
// (target: local/vm/every external substrate) — RCA'd live via check-builder-vm
// (deploy-check-builder-member reported PASS with a zero-step compiled plan). BuilderConfig must
// be set UNCONDITIONALLY, matching NewSpecResolvedBox/resolveBoxSelection's box-construction path.
func TestSyntheticHostBoxFromEnvelope_SetsBuilderConfig(t *testing.T) {
	builder := map[string]*spec.Builder{"npm": {}}
	img := syntheticHostBoxFromEnvelope(nil, builder)
	if img.BuilderConfig == nil {
		t.Fatal("BuilderConfig is nil — compileBuilderSteps will silently skip every builder candy")
	}
	if _, ok := img.BuilderConfig.Builder["npm"]; !ok {
		t.Errorf("BuilderConfig.Builder missing the passed-through npm def: %+v", img.BuilderConfig.Builder)
	}

	// Even with a nil/empty builder map (no project builder vocabulary resolved), the STRUCT
	// itself must be non-nil — matching NewSpecResolvedBox's own unconditional-set behavior — so
	// a future caller with a real vocabulary isn't silently gated by an early "img.BuilderConfig
	// == nil" bail-out elsewhere in the compiler.
	imgEmpty := syntheticHostBoxFromEnvelope(nil, nil)
	if imgEmpty.BuilderConfig == nil {
		t.Error("BuilderConfig is nil even with a nil builder map — must be a non-nil empty struct")
	}
}

// TestSyntheticHostBoxFromEnvelope_SetsDistroConfig is the regression guard for #118 task #59:
// a synthetic box with a nil DistroDef made CompileSystemPackageSteps early-return zero steps —
// silently skipping EVERY candy's system-package install on a standalone host-adhoc deploy —
// while the DISTINCT distro:-gated ServicePackaged step (reading img.Distro, not img.DistroDef)
// still fired, a masked inconsistency RCA'd live via check-charly-vm (deploy-add failed
// enabling virtqemud.socket because libvirt was never installed). DistroConfig/DistroDef must be
// set UNCONDITIONALLY, matching NewSpecResolvedBox's own box-construction path.
func TestSyntheticHostBoxFromEnvelope_SetsDistroConfig(t *testing.T) {
	distro := map[string]*spec.ResolvedDistro{"fedora": {Format: map[string]*spec.Format{"rpm": {}}}}
	img := syntheticHostBoxFromEnvelope(distro, nil)
	if img.DistroConfig == nil {
		t.Fatal("DistroConfig is nil — CompileSystemPackageSteps will silently skip every candy's packages")
	}

	// A nil/empty distro map must still leave a non-nil DistroConfig (DistroDef legitimately
	// resolves to nil when the host distro isn't in the vocabulary — that's a real "unknown
	// distro" outcome, not the masked-nil-struct bug this guards against).
	imgEmpty := syntheticHostBoxFromEnvelope(nil, nil)
	if imgEmpty.DistroConfig == nil {
		t.Error("DistroConfig is nil even with a nil distro map — must be a non-nil empty struct")
	}
}

// TestBuildVmSyntheticBox_SetsBuilderConfig mirrors the host-box regression guard above for the
// vm-adhoc synthetic box, covering BOTH branches (root fallback via syntheticHostBoxFromEnvelope,
// and the non-root user branch's own struct literal).
func TestBuildVmSyntheticBox_SetsBuilderConfig(t *testing.T) {
	builder := map[string]*spec.Builder{"npm": {}}
	distro := map[string]*spec.ResolvedDistro{"fedora": {Format: map[string]*spec.Format{"rpm": {}}}}

	rootImg := buildVmSyntheticBox(&spec.ResolvedVm{Source: spec.VmSource{Kind: "bootc"}}, distro, builder)
	if rootImg.BuilderConfig == nil {
		t.Fatal("root-fallback branch: BuilderConfig is nil")
	}
	if _, ok := rootImg.BuilderConfig.Builder["npm"]; !ok {
		t.Errorf("root-fallback branch: BuilderConfig.Builder missing npm: %+v", rootImg.BuilderConfig.Builder)
	}

	userImg := buildVmSyntheticBox(&spec.ResolvedVm{Source: spec.VmSource{Kind: "cloud_image", BaseUser: "fedora", Distro: "fedora"}}, distro, builder)
	if userImg.BuilderConfig == nil {
		t.Fatal("non-root branch: BuilderConfig is nil")
	}
	if _, ok := userImg.BuilderConfig.Builder["npm"]; !ok {
		t.Errorf("non-root branch: BuilderConfig.Builder missing npm: %+v", userImg.BuilderConfig.Builder)
	}
}

// TestBuildVmSyntheticBox_SetsDistroConfig is the regression guard for #118 task #59, covering
// the vm-adhoc synthetic box: BOTH branches (root fallback, and the non-root cloud_image/
// bootstrap branch that resolves img.Pkg/BuildFormats but used to leave DistroConfig/DistroDef
// unset) must resolve a non-nil DistroDef for a distro present in the envelope vocabulary, so
// CompileSystemPackageSteps actually installs the candy's packages (e.g. libvirt) before the
// distro:-gated ServicePackaged step tries to enable a unit that package provides.
func TestBuildVmSyntheticBox_SetsDistroConfig(t *testing.T) {
	distro := map[string]*spec.ResolvedDistro{
		"arch": {Format: map[string]*spec.Format{"pac": {}}},
	}

	rootImg := buildVmSyntheticBox(&spec.ResolvedVm{Source: spec.VmSource{Kind: "bootc"}}, distro, nil)
	if rootImg.DistroConfig == nil {
		t.Fatal("root-fallback branch: DistroConfig is nil")
	}

	userImg := buildVmSyntheticBox(&spec.ResolvedVm{Source: spec.VmSource{Kind: "cloud_image", BaseUser: "arch", Distro: "arch"}}, distro, nil)
	if userImg.DistroConfig == nil {
		t.Fatal("non-root branch: DistroConfig is nil")
	}
	if userImg.DistroDef == nil {
		t.Fatal("non-root branch: DistroDef is nil for a distro present in the envelope vocabulary — CompileSystemPackageSteps will silently skip every candy's packages (the check-charly-vm virtqemud.socket regression)")
	}
	if userImg.Pkg != "pac" {
		t.Errorf("non-root branch: Pkg = %q, want pac", userImg.Pkg)
	}
}
