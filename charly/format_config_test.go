package main

import (
	"github.com/opencharly/sdk/spec"
	"github.com/opencharly/sdk/vmshared"
	"slices"
	"strings"
	"testing"
)

func TestLoadDistroConfigFromFile(t *testing.T) {
	distroCfg, _, _, err := LoadBuildConfigForBox(testdataDir)
	if err != nil {
		t.Fatalf("loading distro config: %v", err)
	}
	if distroCfg == nil || len(distroCfg.Distro) == 0 {
		t.Fatal("expected non-empty distro config")
	}

	// Check fedora exists
	fedora, ok := distroCfg.Distro["fedora"]
	if !ok {
		t.Fatal("expected fedora distro definition")
	}
	if fedora.Bootstrap.InstallCmd == "" {
		t.Error("fedora bootstrap.install_cmd is empty")
	}
	if len(fedora.Bootstrap.CacheMount) == 0 {
		t.Error("fedora bootstrap.cache_mounts is empty")
	}
	if fedora.Bootstrap.CacheMount[0].Dst != "/var/cache/libdnf5" {
		t.Errorf("fedora cache mount = %q, want /var/cache/libdnf5", fedora.Bootstrap.CacheMount[0].Dst)
	}

	// Check fedora has rpm format
	if fedora.Format == nil || fedora.Format["rpm"] == nil {
		t.Fatal("expected fedora to have rpm format")
	}
	rpm := fedora.Format["rpm"]
	if rpm.InstallTemplate == "" {
		t.Error("rpm install_template is empty")
	}
	if len(rpm.CacheMount) == 0 {
		t.Error("rpm cache_mounts is empty")
	}
	if len(rpm.SectionFields) == 0 {
		t.Error("rpm section_fields is empty")
	}

	// Check ubuntu inherits debian (including formats)
	ubuntu, ok := distroCfg.Distro["ubuntu"]
	if !ok {
		t.Fatal("expected ubuntu distro definition")
	}
	if ubuntu.Inherits != "debian" {
		t.Errorf("ubuntu.inherits = %q, want debian", ubuntu.Inherits)
	}

	// Test ResolveDistro
	resolved := distroCfg.ResolveDistro([]string{"fedora:43", "fedora"})
	if resolved == nil {
		t.Fatal("ResolveDistro returned nil for fedora:43")
	}
	if resolved.Bootstrap.InstallCmd != fedora.Bootstrap.InstallCmd {
		t.Error("ResolveDistro did not resolve to fedora")
	}

	// Test inherits resolution includes formats
	resolvedUbuntu := distroCfg.ResolveDistro([]string{"ubuntu"})
	if resolvedUbuntu == nil {
		t.Fatal("ResolveDistro returned nil for ubuntu")
	}
	if resolvedUbuntu.Bootstrap.InstallCmd == "" {
		t.Error("ubuntu should inherit debian's bootstrap install_cmd")
	}
	if resolvedUbuntu.Format == nil || resolvedUbuntu.Format["deb"] == nil {
		t.Error("ubuntu should inherit debian's deb format")
	}

	// Check arch has both pac and aur formats
	archResolved := distroCfg.ResolveDistro([]string{"arch"})
	if archResolved == nil {
		t.Fatal("ResolveDistro returned nil for arch")
	}
	if archResolved.Format["pac"] == nil {
		t.Error("arch should have pac format")
	}
	if archResolved.Format["aur"] == nil {
		t.Error("arch should have aur format")
	}
}

func TestAllFormatNames(t *testing.T) {
	dc := testDistroConfig()
	names := dc.AllFormatNames()
	if len(names) != 4 {
		t.Errorf("expected 4 format names, got %d: %v", len(names), names)
	}
	// Should be sorted
	if names[0] != "aur" || names[1] != "deb" || names[2] != "pac" || names[3] != "rpm" {
		t.Errorf("format names not sorted: %v", names)
	}
}

func TestValidFormat(t *testing.T) {
	dc := testDistroConfig()
	for _, name := range []string{"rpm", "deb", "pac", "aur"} {
		if !dc.ValidFormat(name) {
			t.Errorf("expected format %q to be valid", name)
		}
	}
	if dc.ValidFormat("apk") {
		t.Error("apk should not be valid in default config")
	}
}

func TestLoadBuilderConfigFromFile(t *testing.T) {
	_, builderCfg, _, err := LoadBuildConfigForBox(testdataDir)
	if err != nil {
		t.Fatalf("loading builder config: %v", err)
	}
	if builderCfg == nil || len(builderCfg.Builder) == 0 {
		t.Fatal("expected non-empty builder config")
	}

	// Check all four builders exist (pixi, npm, cargo, aur)
	for _, name := range []string{"pixi", "npm", "cargo", "aur"} {
		if !builderCfg.ValidBuilderType(name) {
			t.Errorf("expected builder %q to be valid", name)
		}
	}

	// Check pixi detect files
	pixi := builderCfg.Builder["pixi"]
	if len(pixi.DetectFiles) == 0 {
		t.Error("pixi detect_files is empty")
	}
	// Check cargo is inline
	cargo := builderCfg.Builder["cargo"]
	if !cargo.Inline {
		t.Error("cargo should be inline")
	}
	if !cargo.RequiresSrcDir {
		t.Error("cargo should require src dir")
	}
}

func TestBuilderNames(t *testing.T) {
	_, builderCfg, _, _ := LoadBuildConfigForBox(testdataDir)
	names := builderCfg.BuilderNames()
	// The embedded default build vocabulary contributes its builders too
	// (debootstrap/pacstrap beyond testdata's pixi/npm/cargo/aur), so assert the
	// testdata builders are PRESENT rather than pinning an exact count.
	for _, want := range []string{"pixi", "npm", "cargo", "aur"} {
		found := slices.Contains(names, want)
		if !found {
			t.Errorf("builder %q missing from %v", want, names)
		}
	}
}

// TestLegacyTopLevelFormatKeyRejected guards the hard cutover: the legacy
// top-level package-format keys (`rpm:`/`deb:`/`pac:`) — and top-level distro-tag
// keys (`debian:13:`, `debian,ubuntu:`) — are no longer a package surface. Package
// declarations live ONLY under the `distro:` map. A stray top-level `rpm:` is now
// an unknown-key error pointing at `charly migrate`, not a silently-parsed section.
func TestLegacyTopLevelFormatKeyRejected(t *testing.T) {
	RegisterBuildVocabulary(testDistroConfig())

	for _, tc := range []struct{ name, yaml string }{
		{"format-key", "rpm:\n  package:\n    - vim\n"},
		{"colon-tag", "debian:13:\n  package:\n    - vim\n"},
		{"compound-tag", "debian,ubuntu:\n  package:\n    - vim\n"},
	} {
		err := candyBodyGuardErr(tc.yaml)
		if err == nil {
			t.Errorf("%s: expected an unknown-top-level-key error for legacy form, got nil", tc.name)
			continue
		}
		if !strings.Contains(err.Error(), "distro:") {
			t.Errorf("%s: error %q should point the format key at the `distro:` map", tc.name, err.Error())
		}
	}
}

func TestAurBuilderDetectConfig(t *testing.T) {
	builderCfg := testBuilderCfg()
	aur := builderCfg.Builder["aur"]
	if aur == nil {
		t.Fatal("expected aur builder definition")
	}
	if aur.DetectConfig != "aur" {
		t.Errorf("aur detect_config = %q, want \"aur\"", aur.DetectConfig)
	}
}

// ResolveFormatConfigData tests removed — the helper was deleted as part of
// the unified-cutover (format_config: field replaced by charly.yml's
// includes: mechanism).

func TestLoadBuildConfigForImageFallback(t *testing.T) {
	// Post-unified-cutover there is no per-image / per-default fallback — the
	// unified loader reads charly.yml in the project directory. This test
	// now verifies that reading via LoadBuildConfigForBox(dir) produces the
	// same config twice (i.e., is deterministic and idempotent).
	distroCfg, builderCfg, _, err := LoadBuildConfigForBox(testdataDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if distroCfg == nil || len(distroCfg.Distro) == 0 {
		t.Error("expected distro config from charly.yml")
	}
	if builderCfg == nil || len(builderCfg.Builder) == 0 {
		t.Error("expected builder config from charly.yml")
	}

	distroCfg2, _, _, err := LoadBuildConfigForBox(testdataDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if distroCfg2 == nil || len(distroCfg2.Distro) == 0 {
		t.Error("expected distro config from default ref")
	}
}

func TestDnfConfigParse(t *testing.T) {
	var d spec.ResolvedDistro
	if err := decodeViaCUEForTest(t, "dnf:\n  max_parallel_downloads: 10\n  fastestmirror: true\n", &d); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if d.Dnf == nil {
		t.Fatal("expected Dnf parsed")
	}
	if d.Dnf.MaxParallelDownloads != 10 || !d.Dnf.Fastestmirror {
		t.Errorf("Dnf = %+v, want {10 true}", *d.Dnf)
	}
}

// TestDnfConfigInherit verifies a child distro inherits the parent's Dnf when
// it declares none, and its own Dnf wins when set — same per-field merge as
// the other DistroDef sub-blocks (BaseUser, Pacstrap, …).
func TestDnfConfigInherit(t *testing.T) {
	dc := &DistroConfig{Distro: map[string]*spec.ResolvedDistro{
		"fedora": {
			Bootstrap: vmshared.BootstrapDef{InstallCmd: "dnf install -y"},
			Dnf:       &vmshared.DnfConfig{MaxParallelDownloads: 10, Fastestmirror: true},
		},
		"fedora-child":  {Inherits: "fedora"},                                                    // no own Dnf → inherits
		"fedora-child2": {Inherits: "fedora", Dnf: &vmshared.DnfConfig{MaxParallelDownloads: 3}}, // own Dnf wins
	}}

	got := dc.ResolveDistro([]string{"fedora-child"})
	if got == nil || got.Dnf == nil {
		t.Fatal("expected inherited Dnf on child")
	}
	if got.Dnf.MaxParallelDownloads != 10 || !got.Dnf.Fastestmirror {
		t.Errorf("inherited Dnf = %+v, want {10 true}", *got.Dnf)
	}

	got2 := dc.ResolveDistro([]string{"fedora-child2"})
	if got2 == nil || got2.Dnf == nil || got2.Dnf.MaxParallelDownloads != 3 {
		t.Errorf("child's own Dnf should win, got %+v", got2.Dnf)
	}
}

// TestDistroDefPrimaryFormat proves PrimaryFormat returns the base format
// (rpm/deb/pac), skipping the secondary `aur` builder format, deterministically.
func TestDistroDefPrimaryFormat(t *testing.T) {
	arch := &spec.ResolvedDistro{Format: map[string]*FormatDef{"pac": {}, "aur": {Secondary: true}}}
	if got := arch.PrimaryFormat(); got != "pac" {
		t.Errorf("arch PrimaryFormat = %q, want pac (aur is secondary)", got)
	}
	fedora := &spec.ResolvedDistro{Format: map[string]*FormatDef{"rpm": {}}}
	if got := fedora.PrimaryFormat(); got != "rpm" {
		t.Errorf("fedora PrimaryFormat = %q, want rpm", got)
	}
	if got := (&spec.ResolvedDistro{Format: map[string]*FormatDef{"aur": {Secondary: true}}}).PrimaryFormat(); got != "" {
		t.Errorf("aur-only PrimaryFormat = %q, want empty (no base format)", got)
	}
	if got := (*spec.ResolvedDistro)(nil).PrimaryFormat(); got != "" {
		t.Errorf("nil PrimaryFormat = %q, want empty", got)
	}
}

// TestFormatForDistroID proves the SINGLE distroIDToFormat table (consulted by
// FormatHint) maps OS-release IDs to package formats.
func TestFormatForDistroID(t *testing.T) {
	cases := map[string]string{
		"fedora": "rpm", "rhel": "rpm", "ubuntu": "deb", "debian": "deb",
		"arch": "pac", "cachyos": "pac", "endeavouros": "pac", "unknown-distro": "",
	}
	for id, want := range cases {
		if got := vmshared.FormatForDistroID(id); got != want {
			t.Errorf("formatForDistroID(%q) = %q, want %q", id, got, want)
		}
	}
	// FormatHint walks ID then ID_LIKE through the same table.
	hd := &vmshared.HostDistro{ID: "weird", IDLike: []string{"arch"}}
	if got := hd.FormatHint(); got != "pac" {
		t.Errorf("FormatHint via ID_LIKE = %q, want pac", got)
	}
}

// TestDistroConfigFindFormat proves FindFormat resolves a format across distros
// (inherits-aware) and that the real build.yml pac format carries the host cell.
func TestDistroConfigFindFormat(t *testing.T) {
	dc, _, _, err := LoadBuildConfigForBox(repoRootDir(t))
	if err != nil {
		t.Fatalf("LoadBuildConfigForBox: %v", err)
	}
	for _, f := range []string{"rpm", "deb", "pac"} {
		fd := dc.FindFormat(f)
		if fd == nil {
			t.Errorf("FindFormat(%q) = nil, want a FormatDef", f)
			continue
		}
		if formatPhaseTemplate(fd, PhaseInstall, VenueHostNative) == "" {
			t.Errorf("format %q has no phase.install.host cell", f)
		}
		if fd.UninstallTemplate == "" {
			t.Errorf("format %q has no uninstall_template", f)
		}
	}
	if dc.FindFormat("nonexistent") != nil {
		t.Error("FindFormat(nonexistent) should be nil")
	}
}
