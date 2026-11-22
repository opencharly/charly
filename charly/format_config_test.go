package main

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/opencharly/spec/hostenv"
	"github.com/opencharly/spec/spec"
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
	if rpm.Phases == nil || rpm.Phases.Install == nil || rpm.Phases.Install.Container == "" {
		t.Error("rpm phase.install.container is empty")
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
	if archResolved.Format["aur"] != nil {
		t.Error("arch declares an aur format: aur is a builder (builder.aur), not a distro format")
	}
}

func TestAllFormatNames(t *testing.T) {
	dc := testDistroConfig()
	names := dc.AllFormatNames()
	// apk joined the vocabulary with the alpine distro entity.
	// aur is a builder, never a distro format (opencharly/sdk#608).
	want := []string{"apk", "deb", "pac", "rpm"}
	if len(names) != len(want) {
		t.Errorf("expected %d format names, got %d: %v", len(want), len(names), names)
	}
	// Should be sorted
	for i := range want {
		if i >= len(names) || names[i] != want[i] {
			t.Errorf("format names not sorted or incomplete: got %v, want %v", names, want)
			break
		}
	}
}

func TestValidFormat(t *testing.T) {
	dc := testDistroConfig()
	// apk is valid since the alpine distro entity declares it.
	for _, name := range []string{"rpm", "deb", "pac", "apk"} {
		if !dc.ValidFormat(name) {
			t.Errorf("expected format %q to be valid", name)
		}
	}
	// aur is a build-time manager, not a box build format.
	if dc.ValidFormat("aur") {
		t.Error("aur is still a valid build format")
	}
	// Negative control — an nFPM format the build vocabulary does NOT declare.
	// Without a rejected case this test could not tell ValidFormat from a
	// function that returns true for everything.
	if dc.ValidFormat("ipk") {
		t.Error("ipk should not be valid in default config (no distro declares it)")
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

// TestEmbeddedBuildersAuthorNoInstallBehaviour proves the embedded builder nodes stay authored DATA
// only. What a builder installs is the builder PLUGIN's own knowledge (kit), never the authored node,
// whose DESIGN shape is a manager map (DESIGN.cue #BuilderAlt under D-PLUG-2; opencharly/sdk#608). The
// authored `copy_artifact`/`copy_binary`/`detect_config` fields no longer EXIST (opencharly/spec#421),
// so the absence is now a property of the schema rather than of this vocabulary: what is left to
// assert here is the aur host template, which guards its own output instead of swallowing a failure
// with `|| true`.
func TestEmbeddedBuildersAuthorNoInstallBehaviour(t *testing.T) {
	builderCfg := testBuilderCfg()
	for _, word := range []string{"aur", "npm", "pixi", "mise", "cargo"} {
		if builderCfg.Builder[word] == nil {
			t.Fatalf("expected a %s builder definition", word)
		}
	}
	if strings.Contains(spec.BuilderPhaseTemplate(builderCfg.Builder["aur"], spec.PhaseInstall, spec.VenueHostNative), "|| true") {
		t.Error("the aur host template swallows a failure with `|| true`")
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
	dc := &spec.DistroConfig{Distro: map[string]*spec.ResolvedDistro{
		"fedora": {
			Bootstrap: spec.Bootstrap{InstallCmd: "dnf install -y"},
			Dnf:       &spec.Dnf{MaxParallelDownloads: 10, Fastestmirror: true},
		},
		"fedora-child":  {Inherits: "fedora"},                                          // no own Dnf → inherits
		"fedora-child2": {Inherits: "fedora", Dnf: &spec.Dnf{MaxParallelDownloads: 3}}, // own Dnf wins
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

// TestDistroDefPrimaryFormat proves PrimaryFormat returns the distro's OS package format
// deterministically, and that the embedded arch distro declares no `aur` format: aur is a
// builder, never a distro format (opencharly/sdk#608).
func TestDistroDefPrimaryFormat(t *testing.T) {
	arch := &spec.ResolvedDistro{Format: map[string]*spec.Format{"pac": {}}}
	if got := arch.PrimaryFormat(); got != "pac" {
		t.Errorf("arch PrimaryFormat = %q, want pac", got)
	}
	fedora := &spec.ResolvedDistro{Format: map[string]*spec.Format{"rpm": {}}}
	if got := fedora.PrimaryFormat(); got != "rpm" {
		t.Errorf("fedora PrimaryFormat = %q, want rpm", got)
	}
	if got := (&spec.ResolvedDistro{Format: map[string]*spec.Format{}}).PrimaryFormat(); got != "" {
		t.Errorf("format-less PrimaryFormat = %q, want empty", got)
	}
	if cfg := testDistroConfig(); cfg.Distro["arch"] != nil && cfg.Distro["arch"].Format["aur"] != nil {
		t.Error("the arch distro still declares an `aur` format: aur is a builder (builder.aur), not a distro format")
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
		if got := hostenv.FormatForDistroID(id); got != want {
			t.Errorf("formatForDistroID(%q) = %q, want %q", id, got, want)
		}
	}
	// FormatHint walks ID then ID_LIKE through the same table.
	hd := &hostenv.HostDistro{ID: "weird", IDLike: []string{"arch"}}
	if got := hd.FormatHint(); got != "pac" {
		t.Errorf("FormatHint via ID_LIKE = %q, want pac", got)
	}
}

// TestRpmHostCellHandlesRepos proves the rpm phase.install.host cell carries the
// repo setup the container cell has always had: the .repo file write (with the
// gpgkey), the key import, and --enable-repo on the install line. Regression for
// the check-fedora-vm `No match for argument: charly` — the host cell was bare
// `dnf install -y ...` with no repo handling, so a candy's distro repo was never
// added on the host/VM venue. (Presence check only — the full render lives in the
// sdk repo's buildkit/render_test.go with a literal host-cell fixture, because
// charly/ core must not import sdk.)
func TestRpmHostCellHandlesRepos(t *testing.T) {
	dc, _, _, err := LoadBuildConfigForBox(repoRootDir(t))
	if err != nil {
		t.Fatalf("LoadBuildConfigForBox: %v", err)
	}
	fd := dc.FindFormat("rpm")
	if fd == nil {
		t.Fatal("FindFormat(rpm) = nil")
	}
	tmpl := spec.FormatPhaseTemplate(fd, spec.PhaseInstall, spec.VenueHostNative)
	if tmpl == "" {
		t.Fatal("rpm format has no phase.install.host cell")
	}
	for _, want := range []string{
		"/etc/yum.repos.d/{{.name}}.repo",
		"rpm --import {{.gpgkey}}",
		"--enable-repo={{quote .name}}",
	} {
		if !strings.Contains(tmpl, want) {
			t.Errorf("rpm host cell missing %q; got:\n%s", want, tmpl)
		}
	}
}

// TestPacHostCellHandlesRepos proves the pac phase.install cell carries the
// Keys/Repos setup — the repo appended to /etc/pacman.conf + key import — so a
// candy's distro repo resolves on BOTH venues. Regression for the
// check-cachyos-vm `target not found: charly` (the old host cell was bare
// `pacman -Syu ...` with no repo handling). The R3 shape: ONE venue-agnostic
// install body; the container venue gets the RUN {{cacheMounts}} wrapper from
// FormatPhaseTemplate, the host venue runs it verbatim. The body supports a
// repo `key:` that is either a published key FILE URL (curl + pacman-key --add,
// deterministic and keyserver-free) or a keyserver fingerprint. Presence check
// only — the full render lives in the sdk repo's buildkit/render_test.go
// (charly/ core must not import sdk).
func TestPacHostCellHandlesRepos(t *testing.T) {
	dc, _, _, err := LoadBuildConfigForBox(repoRootDir(t))
	if err != nil {
		t.Fatalf("LoadBuildConfigForBox: %v", err)
	}
	fd := dc.FindFormat("pac")
	if fd == nil {
		t.Fatal("FindFormat(pac) = nil")
	}
	tmpl := spec.FormatPhaseTemplate(fd, spec.PhaseInstall, spec.VenueHostNative)
	if tmpl == "" {
		t.Fatal("pac format has no phase.install cell")
	}
	for _, want := range []string{
		"/etc/pacman.conf",
		"pacman-key --init",
		"pacman-key --recv-keys {{.key}}",
		"curl -fsSL {{quote .key}} -o /tmp/{{.name}}.gpg && pacman-key --add",
		"Server = {{.server}}",
	} {
		if !strings.Contains(tmpl, want) {
			t.Errorf("pac install body missing %q; got:\n%s", want, tmpl)
		}
	}
	// The container venue must wrap the SAME body with the cacheMounts RUN prefix
	// (R3 — never a second copy).
	containerTmpl := spec.FormatPhaseTemplate(fd, spec.PhaseInstall, spec.VenueContainerBuilder)
	if !strings.Contains(containerTmpl, "RUN {{cacheMounts .CacheMounts}} \\\n") {
		t.Errorf("pac container venue missing the RUN cacheMounts wrapper; got:\n%s", containerTmpl)
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
			t.Errorf("FindFormat(%q) = nil, want a spec.Format", f)
			continue
		}
		if spec.FormatPhaseTemplate(fd, spec.PhaseInstall, spec.VenueHostNative) == "" {
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

// TestPacInstallUpgradeSemantics pins the two properties of the pac install body that
// still matter now that the Omarchy update-guard bypass is gone: the transaction is an
// UPGRADE (`-Syu`, not a partial `-Sy`), and the local-package path installs with `-U`.
//
// The `-u` half is the one worth protecting: dropping it would make every charly package
// install a partial upgrade, which is worse than a loud failure.
func TestPacInstallUpgradeSemantics(t *testing.T) {
	dc, _, _, err := LoadBuildConfigForBox(repoRootDir(t))
	if err != nil {
		t.Fatalf("LoadBuildConfigForBox: %v", err)
	}
	fd := dc.FindFormat("pac")
	if fd == nil {
		t.Fatal("FindFormat(pac) = nil")
	}
	tmpl := spec.FormatPhaseTemplate(fd, spec.PhaseInstall, spec.VenueHostNative)
	if !strings.Contains(tmpl, "pacman -Syu --noconfirm --needed") {
		t.Errorf("pac install body no longer runs `pacman -Syu --noconfirm --needed`; `-Sy` alone "+
			"is a partial upgrade. got:\n%s", tmpl)
	}
	if fd.LocalPkg == nil || fd.LocalPkg.InstallTemplate == "" {
		t.Fatal("pac format has no local_pkg.install_template")
	}
	if !strings.Contains(fd.LocalPkg.InstallTemplate, "pacman -U --noconfirm") {
		t.Errorf("pac local_pkg install must use `pacman -U`. got:\n%s", fd.LocalPkg.InstallTemplate)
	}

	// NO comment line may survive inside the rendered shell body. The body is
	// `&& \`-continued, so a `#` line swallows the rest of the LOGICAL line and the
	// command arrives truncated — the RUN then fails with no stderr reaching the build
	// log (measured: "pacman-key --init && # Reseed pacman keyring ..." never reached
	// pacman, and every box built on that base failed while reporting nothing).
	for _, body := range []struct{ name, text string }{
		{"install", tmpl},
		{"local_pkg.install", fd.LocalPkg.InstallTemplate},
	} {
		for _, line := range strings.Split(body.text, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				t.Errorf("%s template emits a comment line into the shell body, which truncates a "+
					"`&& \\`-continued command: %q", body.name, strings.TrimSpace(line))
			}
		}
	}
}

// TestPacstrapBuilderTemplateFieldsExist proves every `.Distro.Pacstrap.X` interpolated
// anywhere in this repo's manifest is a real field on spec.Pacstrap.
//
// This guards a CLASS, not a string. Go templates resolve struct fields at RENDER time, so a
// reference to a field that no longer exists compiles, validates, passes every unit test —
// and then fails at `charly vm build <bootstrap-vm>`, a path no CI gate exercises:
//
//	template: bootstrap-script:2:9: executing "bootstrap-script"
//	at <.Distro.Pacstrap.KeyringInitCmd>: can't evaluate field KeyringInitCmd
//	in type *spec.Pacstrap
//
// That is exactly what happened. charly#484 dropped the dead `keyring_init_cmd` field and its
// authored values, correctly verifying it had zero *Go* consumers — and missed this TEMPLATE
// consumer. Every pacstrap VM build in the repo was broken from that merge until the fix,
// arch and cachyos included, because the builder template is shared by all of them.
//
// Reflecting over the struct rather than listing allowed names is deliberate: a hand-kept list
// would have to be updated by the same person who forgot the template, which is no guard.
func TestPacstrapBuilderTemplateFieldsExist(t *testing.T) {
	manifest := filepath.Join(repoRootDir(t), "charly.yml")
	body, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatalf("reading %s: %v", manifest, err)
	}

	real := map[string]bool{}
	pt := reflect.TypeOf(spec.Pacstrap{})
	names := make([]string, 0, pt.NumField())
	for i := 0; i < pt.NumField(); i++ {
		real[pt.Field(i).Name] = true
		names = append(names, pt.Field(i).Name)
	}

	re := regexp.MustCompile(`\.Distro\.Pacstrap\.([A-Za-z0-9_]+)`)
	matches := re.FindAllSubmatch(body, -1)
	for _, m := range matches {
		field := string(m[1])
		if !real[field] {
			t.Errorf("charly.yml interpolates .Distro.Pacstrap.%s, which is NOT a field of "+
				"spec.Pacstrap. Every `charly vm build` of a bootstrap VM will fail at template "+
				"render, on a path no unit test reaches. spec.Pacstrap has: %v", field, names)
		}
	}
	t.Logf("checked %d .Distro.Pacstrap.* references against spec.Pacstrap %v", len(matches), names)
}
