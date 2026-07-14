package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencharly/sdk/spec"
	"gopkg.in/yaml.v3"
)

// testPacLocalPkgDef returns a LocalPkgDef mirroring build.yml's `pac.local_pkg`
// block — the config that drives the localpkg mechanism. Tests use it so they
// exercise the SAME config-driven path the loader produces, without parsing YAML.
func testPacLocalPkgDef() *LocalPkgDef {
	return &LocalPkgDef{
		PkgGlob:         "*.pkg.tar.zst",
		SourceSentinel:  "PKGBUILD",
		BuildTemplate:   "cd {{.SrcDir}} && PKGDEST={{.PkgDest}} makepkg -sf --noconfirm",
		InstallTemplate: "pacman -U --noconfirm {{.StageDir}}/{{.Glob}}",
		Probe:           "command -v pacman",
		DepBuilder:      "aur",
	}
}

// testPacDistroDef returns a DistroDef whose `pac` format carries the localpkg
// contract — so compileLocalPkgStep resolves it the way it would from build.yml.
func testPacDistroDef() *DistroDef {
	return &DistroDef{
		Format: map[string]*FormatDef{
			"pac": {LocalPkg: testPacLocalPkgDef()},
		},
	}
}

// TestCompileLocalPkgStep verifies the per-format `localpkg:` map compiles into a
// single LocalPkgInstallStep carrying the format-matched source ref + anchors +
// the config-driven LocalPkg; a candy with no source for the target format, or a
// distro with no localpkg-capable format, compiles to nothing.
func TestCompileLocalPkgStep(t *testing.T) {
	img := &ResolvedBox{
		Name:      "charly-host",
		Pkg:       "pac",
		DistroDef: testPacDistroDef(),
		Builder:   map[string]string{"aur": "ghcr.io/opencharly/arch-builder:latest"},
	}
	hostCtx := HostContext{MachineVenue: true, Distro: "arch"}

	// A candy with no localpkg entry for the target format → nil.
	if step := compileLocalPkgStep(&Candy{Name: "no-pkg"}, img, hostCtx); step != nil {
		t.Errorf("candy with no localpkg: should compile to nil, got %T", step)
	}

	// The charly candy's per-format map: pac resolves to pkg/arch.
	l := &Candy{Name: "charly", SourceDir: "/layers/charly", localpkg: map[string]string{"pac": "pkg/arch", "rpm": "pkg/fedora", "deb": "pkg/debian"}}
	step := compileLocalPkgStep(l, img, hostCtx)
	if step == nil {
		t.Fatal("compileLocalPkgStep returned nil for a candy with a pac localpkg source")
	}
	pkg, ok := step.(*LocalPkgInstallStep)
	if !ok {
		t.Fatalf("compileLocalPkgStep returned %T, want *LocalPkgInstallStep", step)
	}
	if pkg.PkgbuildRef != "pkg/arch" || pkg.CandyName != "charly" || pkg.CandyDir != "/layers/charly" {
		t.Errorf("step fields = %+v", pkg)
	}
	if pkg.ProjectDir == "" {
		t.Error("ProjectDir should be set from os.Getwd()")
	}
	// Format + LocalPkg config resolved from the distro's pac format (config-driven).
	if pkg.Format != "pac" || pkg.LocalPkg == nil || pkg.LocalPkg.PkgGlob != "*.pkg.tar.zst" {
		t.Errorf("LocalPkg config not resolved from the pac format: Format=%q LocalPkg=%#v", pkg.Format, pkg.LocalPkg)
	}

	// Same candy on an rpm distro → picks the rpm source from the map.
	rpmImg := &ResolvedBox{Name: "charly-fedora", Pkg: "rpm", DistroDef: &DistroDef{Format: map[string]*FormatDef{
		"rpm": {LocalPkg: &LocalPkgDef{PkgGlob: "*.rpm", SourceSentinel: "*.spec", BuildTemplate: "x", InstallTemplate: "dnf install -y {{.StageDir}}/{{.Glob}}", Probe: "command -v dnf"}},
	}}}
	if rs, ok := compileLocalPkgStep(l, rpmImg, hostCtx).(*LocalPkgInstallStep); !ok || rs.Format != "rpm" || rs.PkgbuildRef != "pkg/fedora" {
		t.Errorf("rpm distro should pick pkg/fedora via the format map, got %#v", compileLocalPkgStep(l, rpmImg, hostCtx))
	}

	// Distro with a format but NO localpkg block → nil (no native package).
	noFmt := compileLocalPkgStep(l, &ResolvedBox{Name: "charly-x", Pkg: "rpm", DistroDef: &DistroDef{Format: map[string]*FormatDef{"rpm": {}}}}, hostCtx)
	if noFmt != nil {
		t.Errorf("distro without a localpkg-capable format should compile to nil, got %#v", noFmt)
	}
}

// TestLocalPkgInstallStepIR exercises the IR contract: kind, scope (system),
// venue (host-native), gate (none), reverse (no ledger ops — like apk).
func TestLocalPkgInstallStepIR(t *testing.T) {
	s := &LocalPkgInstallStep{PkgbuildRef: "pkg/arch", CandyName: "charly"}
	if s.Kind() != StepKindLocalPkgInstall {
		t.Errorf("Kind() = %q, want %q", s.Kind(), StepKindLocalPkgInstall)
	}
	if s.Scope() != ScopeSystem {
		t.Errorf("Scope() = %v, want ScopeSystem", s.Scope())
	}
	if s.Venue() != VenueHostNative {
		t.Errorf("Venue() = %v, want VenueHostNative", s.Venue())
	}
	if s.RequiresGate() != GateNone {
		t.Errorf("RequiresGate() = %v, want GateNone", s.RequiresGate())
	}
	if s.Reverse() != nil {
		t.Errorf("Reverse() = %v, want nil (OS package is the substrate's own, not ledger-reversed)", s.Reverse())
	}
}

// TestBuildDeployPlanLocalPkgOrdering proves the localpkg step is emitted BEFORE
// the candy's task steps in the compiled plan — load-bearing so the charly candy's
// package-aware cmd: gate sees charly already installed and does nothing
// (instead of curling a /usr/local/bin/charly that shadows /usr/bin/charly).
func TestBuildDeployPlanLocalPkgOrdering(t *testing.T) {
	l := &Candy{
		Name:     "charly",
		localpkg: map[string]string{"pac": "pkg/arch"},
		plan: []Step{
			{Run: "build", Op: Op{Plugin: "command", PluginInput: map[string]any{"command": "echo install charly"}, RunAs: "root"}},
		},
	}
	img := &ResolvedBox{Name: "host-adhoc", Home: "/root", User: "root", Pkg: "pac", DistroDef: testPacDistroDef()}
	plan, err := BuildDeployPlan(l, img, HostContext{MachineVenue: true, Distro: "arch"})
	if err != nil {
		t.Fatalf("BuildDeployPlan: %v", err)
	}
	pkgIdx, taskIdx := -1, -1
	for i, step := range plan.Steps {
		switch step.(type) {
		case *LocalPkgInstallStep:
			if pkgIdx < 0 {
				pkgIdx = i
			}
		case *OpStep:
			if taskIdx < 0 {
				taskIdx = i
			}
		}
	}
	if pkgIdx < 0 {
		t.Fatal("no LocalPkgInstallStep in the compiled plan")
	}
	if taskIdx < 0 {
		t.Fatal("no OpStep in the compiled plan")
	}
	if pkgIdx > taskIdx {
		t.Errorf("localpkg step (idx %d) must precede the candy's task steps (idx %d) so the cmd: gate sees the installed package", pkgIdx, taskIdx)
	}
}

// TestOCITargetLocalPkgNilContractEmitsNothing proves a localpkg step with NO LocalPkg
// contract (LocalPkg==nil — a distro with no localpkg-capable format) renders nothing at
// image build. Post-C1.4 the build-emit routes through the FULL plugin chain
// (emitStep → pluginEmitStepWords[LocalPkgInstall]="local-pkg-install" → spliceClassStepEmit →
// candy/plugin-installstep OpEmit → emitViaHostBuild → HostBuild("step-emit") →
// stepEmitLocalPkgInstall → renderLocalPkgImageInstall), which returns "" for a nil LocalPkg —
// so emitStep succeeds and emits nothing.
func TestOCITargetLocalPkgNilContractEmitsNothing(t *testing.T) {
	tgt := &OCITarget{}
	step := &LocalPkgInstallStep{PkgbuildRef: "pkg/arch", CandyName: "charly"}
	if err := tgt.emitStep(step, &InstallPlan{}); err != nil {
		t.Fatalf("OCITarget.emitStep(LocalPkgInstallStep, nil LocalPkg) = %v, want nil", err)
	}
	if tgt.buf.Len() != 0 {
		t.Errorf("OCITarget emitted %q for a nil-LocalPkg localpkg step; should emit nothing", tgt.buf.String())
	}
}

// TestResolveLocalPkgDir covers source-dir resolution across the four branches
// (absolute, candy-relative, project-relative, walk-up) AND the config-driven
// per-format sentinel: PKGBUILD (plain file), *.spec (glob), debian/control
// (sub-path). A missing sentinel returns "".
func TestResolveLocalPkgDir(t *testing.T) {
	root := t.TempDir()
	// <root>/pkg/arch/PKGBUILD (superproject) and a nested project dir.
	pkgArch := filepath.Join(root, "pkg", "arch")
	if err := os.MkdirAll(pkgArch, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgArch, "PKGBUILD"), []byte("pkgname=opencharly-git\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	nestedProject := filepath.Join(root, "image", "cachyos")
	if err := os.MkdirAll(nestedProject, 0o755); err != nil {
		t.Fatal(err)
	}
	// A candy dir that bundles its OWN PKGBUILD (candy-relative branch).
	candyWithPkg := filepath.Join(root, "candy", "mytool")
	if err := os.MkdirAll(filepath.Join(candyWithPkg, "arch"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(candyWithPkg, "arch", "PKGBUILD"), []byte("pkgname=mytool\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// rpm source dir (sentinel is a *.spec glob) and deb source dir (sentinel is
	// a debian/control sub-path) — proving the generic sentinel match.
	pkgFedora := filepath.Join(root, "pkg", "fedora")
	if err := os.MkdirAll(pkgFedora, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgFedora, "opencharly.spec"), []byte("Name: opencharly\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pkgDebian := filepath.Join(root, "pkg", "debian", "debian")
	if err := os.MkdirAll(pkgDebian, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDebian, "control"), []byte("Source: opencharly\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 1. Absolute ref (PKGBUILD sentinel).
	if got := resolveLocalPkgDir(pkgArch, "", "", "PKGBUILD"); got != pkgArch {
		t.Errorf("absolute ref = %q, want %q", got, pkgArch)
	}
	// 2. Candy-relative.
	if got := resolveLocalPkgDir("arch", candyWithPkg, root, "PKGBUILD"); got != filepath.Join(candyWithPkg, "arch") {
		t.Errorf("candy-relative = %q, want %q", got, filepath.Join(candyWithPkg, "arch"))
	}
	// 3. Project-relative (project dir == superproject root).
	if got := resolveLocalPkgDir("pkg/arch", "/no/such/layer", root, "PKGBUILD"); got != pkgArch {
		t.Errorf("project-relative = %q, want %q", got, pkgArch)
	}
	// 4. Walk-up: project dir is the nested box/cachyos; pkg/arch is two levels up.
	if got := resolveLocalPkgDir("pkg/arch", "/no/such/layer", nestedProject, "PKGBUILD"); got != pkgArch {
		t.Errorf("walk-up = %q, want %q (must find the superproject pkg/arch from a nested project dir)", got, pkgArch)
	}
	// 5. rpm glob sentinel (*.spec).
	if got := resolveLocalPkgDir("pkg/fedora", "/no/such/layer", root, "*.spec"); got != pkgFedora {
		t.Errorf("rpm *.spec sentinel = %q, want %q", got, pkgFedora)
	}
	// 6. deb sub-path sentinel (debian/control).
	wantDeb := filepath.Join(root, "pkg", "debian")
	if got := resolveLocalPkgDir("pkg/debian", "/no/such/layer", root, "debian/control"); got != wantDeb {
		t.Errorf("deb debian/control sentinel = %q, want %q", got, wantDeb)
	}
	// 7. Missing sentinel → "".
	if got := resolveLocalPkgDir("does/not/exist", "/no/such/layer", nestedProject, "PKGBUILD"); got != "" {
		t.Errorf("missing sentinel = %q, want empty (no-op fallback)", got)
	}
	// 8. Empty ref → "".
	if got := resolveLocalPkgDir("", candyWithPkg, root, "PKGBUILD"); got != "" {
		t.Errorf("empty ref = %q, want empty", got)
	}
	// 9. Empty sentinel → "" (never matches).
	if got := resolveLocalPkgDir("pkg/arch", "", root, ""); got != "" {
		t.Errorf("empty sentinel = %q, want empty", got)
	}
}

// TestLocalPkgMapRejectsScalar proves the loader hard-rejects the legacy scalar
// form with an `charly migrate` hint, and accepts the per-format map.
func TestLocalPkgMapRejectsScalar(t *testing.T) {
	var m LocalPkgMap
	if err := yaml.Unmarshal([]byte("pkg/arch\n"), &m); err == nil || !strings.Contains(err.Error(), "per-format map") {
		t.Errorf("scalar localpkg should be rejected as a per-format map, got %v", err)
	}
	m = nil
	if err := yaml.Unmarshal([]byte("{pac: pkg/arch, rpm: pkg/fedora}\n"), &m); err != nil {
		t.Fatalf("map form should decode, got %v", err)
	}
	if m["pac"] != "pkg/arch" || m["rpm"] != "pkg/fedora" {
		t.Errorf("decoded map = %v", m)
	}
}

// localPkgRecExec records RunSystem scripts + PutFile dests so the install-body
// tests can assert the transfer+install leg without a real venue.
type localPkgRecExec struct {
	systemScripts []string
	userScripts   []string
	putDests      []string
	probeYes      bool // canned answer for the config-driven package-manager probe
}

func (e *localPkgRecExec) Venue() string { return "localpkg-rec://test" }
func (e *localPkgRecExec) RunSystem(_ context.Context, script string, _ EmitOpts) error {
	e.systemScripts = append(e.systemScripts, script)
	return nil
}
func (e *localPkgRecExec) RunUser(_ context.Context, script string, _ EmitOpts) error {
	e.userScripts = append(e.userScripts, script)
	return nil
}
func (e *localPkgRecExec) RunBuilder(context.Context, BuilderRunOpts) ([]byte, error) {
	return nil, nil
}
func (e *localPkgRecExec) PutFile(_ context.Context, _, remotePath string, _ uint32, _ bool, _ EmitOpts) error {
	e.putDests = append(e.putDests, remotePath)
	return nil
}
func (e *localPkgRecExec) GetFile(context.Context, string, bool, EmitOpts) ([]byte, error) {
	return nil, nil
}
func (e *localPkgRecExec) RunInteractive(context.Context, string) (int, error) {
	return -1, spec.ErrNotSupported
}
func (e *localPkgRecExec) RunStream(context.Context, string) (int, error) {
	return -1, spec.ErrNotSupported
}
func (e *localPkgRecExec) RunCapture(_ context.Context, _ string) (string, string, int, error) {
	// The probe script echoes "yes"/"no"; mirror that contract.
	if e.probeYes {
		return "yes", "", 0, nil
	}
	return "no", "", 0, nil
}
func (e *localPkgRecExec) Kind() string { return "localpkg-rec" }
func (e *localPkgRecExec) ResolveHome(context.Context, string) (string, error) {
	return "/home/guest", nil
}

// TestVenueHasPkgManager confirms the gate runs the format's config-driven probe
// (LocalPkgDef.Probe), treating only an exact "yes" as supported; DryRun assumes
// true; a nil LocalPkgDef gates false (never assume a target can take a package).
func TestVenueHasPkgManager(t *testing.T) {
	lp := testPacLocalPkgDef()
	yes := &localPkgRecExec{probeYes: true}
	if !venueHasPkgManager(context.Background(), yes, lp, EmitOpts{}) {
		t.Error("venue reporting the package manager present should gate true")
	}
	no := &localPkgRecExec{probeYes: false}
	if venueHasPkgManager(context.Background(), no, lp, EmitOpts{}) {
		t.Error("venue without the package manager should gate false")
	}
	// DryRun assumes true regardless of the probe (planner shows what it WOULD do).
	if !venueHasPkgManager(context.Background(), no, lp, EmitOpts{DryRun: true}) {
		t.Error("DryRun should assume the package manager present")
	}
	// Nil LocalPkgDef → false even on DryRun (no format config = nothing to do).
	if venueHasPkgManager(context.Background(), yes, nil, EmitOpts{DryRun: true}) {
		t.Error("nil LocalPkgDef should gate false")
	}
}

// TestExecLocalPkgInstall_SkipsUnsupported proves an unsupported venue is a
// clean no-op: no build, no transfer, no install — the candy's curl/COPY task
// installs it instead.
func TestExecLocalPkgInstall_SkipsUnsupported(t *testing.T) {
	exec := &localPkgRecExec{}
	s := &LocalPkgInstallStep{PkgbuildRef: "pkg/arch", CandyName: "charly", ProjectDir: t.TempDir(), Format: "pac", LocalPkg: testPacLocalPkgDef()}
	if err := execLocalPkgInstall(context.Background(), exec, s, false /* supported */, "host", EmitOpts{}); err != nil {
		t.Fatalf("unsupported venue should be a clean no-op, got %v", err)
	}
	if len(exec.systemScripts) != 0 || len(exec.putDests) != 0 {
		t.Errorf("unsupported venue must not install anything: systemScripts=%v putDests=%v", exec.systemScripts, exec.putDests)
	}
}

// TestExecLocalPkgInstall_SkipsNilLocalPkg proves a step with no resolved
// LocalPkg config (target distro declares no localpkg-capable format) is a clean
// no-op even when the venue is reported supported.
func TestExecLocalPkgInstall_SkipsNilLocalPkg(t *testing.T) {
	exec := &localPkgRecExec{}
	s := &LocalPkgInstallStep{PkgbuildRef: "pkg/arch", CandyName: "charly", ProjectDir: t.TempDir()} // LocalPkg nil
	if err := execLocalPkgInstall(context.Background(), exec, s, true, "host", EmitOpts{}); err != nil {
		t.Fatalf("nil LocalPkg should be a clean no-op, got %v", err)
	}
	if len(exec.systemScripts) != 0 || len(exec.putDests) != 0 {
		t.Errorf("nil LocalPkg must not install anything: systemScripts=%v putDests=%v", exec.systemScripts, exec.putDests)
	}
}

// TestExecLocalPkgInstall_SkipsMissingSource proves a missing source dir on a
// supported venue is ALSO a clean no-op (fallback to the candy's curl/COPY
// task) — not an error that aborts the deploy.
func TestExecLocalPkgInstall_SkipsMissingSource(t *testing.T) {
	exec := &localPkgRecExec{}
	s := &LocalPkgInstallStep{PkgbuildRef: "no/such/source", CandyName: "charly", ProjectDir: t.TempDir(), Format: "pac", LocalPkg: testPacLocalPkgDef()}
	if err := execLocalPkgInstall(context.Background(), exec, s, true /* supported */, "host", EmitOpts{}); err != nil {
		t.Fatalf("missing source should be a clean no-op, got %v", err)
	}
	if len(exec.systemScripts) != 0 || len(exec.putDests) != 0 {
		t.Errorf("missing source must not install anything: systemScripts=%v putDests=%v", exec.systemScripts, exec.putDests)
	}
}

// TestTransferAndInstallPkgs proves the shared transfer+install leg stages the
// dir, PutFiles each package, and renders the format's CONFIG-DRIVEN install
// command (LocalPkgDef.InstallTemplate) against the staging glob — venue-agnostic.
func TestTransferAndInstallPkgs(t *testing.T) {
	exec := &localPkgRecExec{}
	lp := testPacLocalPkgDef()
	pkgs := []string{"/tmp/build/opencharly-git-2026.155.0001-1-x86_64.pkg.tar.zst"}
	if err := transferAndInstallPkgs(context.Background(), exec, lp, pkgs, EmitOpts{}); err != nil {
		t.Fatalf("transferAndInstallPkgs: %v", err)
	}
	if len(exec.putDests) != 1 || !strings.HasPrefix(exec.putDests[0], localPkgGuestStage) {
		t.Errorf("package not staged under %s: %v", localPkgGuestStage, exec.putDests)
	}
	// The install command is rendered from the config template, not hardcoded.
	wantCmd := "pacman -U --noconfirm " + localPkgGuestStage + "/" + lp.PkgGlob
	if len(exec.systemScripts) != 1 || strings.TrimSpace(exec.systemScripts[0]) != wantCmd {
		t.Errorf("install command = %v, want rendered %q", exec.systemScripts, wantCmd)
	}
	// No packages → error (caller bug, never a silent skip).
	if err := transferAndInstallPkgs(context.Background(), exec, lp, nil, EmitOpts{}); err == nil {
		t.Error("transferAndInstallPkgs(nil pkgs) should error")
	}
	// Nil LocalPkgDef → error.
	if err := transferAndInstallPkgs(context.Background(), exec, nil, pkgs, EmitOpts{}); err == nil {
		t.Error("transferAndInstallPkgs(nil LocalPkgDef) should error")
	}
}

// TestBuildLocalPkgOnHost_DryRunAndEmpty proves the build leg renders the
// CONFIG-DRIVEN build template (no hardcoded makepkg) and honors DryRun (no
// shell-out), and that a nil/empty config errors rather than silently building.
func TestBuildLocalPkgOnHost_DryRunAndEmpty(t *testing.T) {
	lp := testPacLocalPkgDef()
	// DryRun: renders the template (proving config-driven) but never runs it.
	if pkgs, err := buildLocalPkgOnHost(context.Background(), lp, "/src/pkg/arch", EmitOpts{DryRun: true}); err != nil || pkgs != nil {
		t.Errorf("dry-run = (%v, %v), want (nil, nil)", pkgs, err)
	}
	// Nil LocalPkgDef → error.
	if _, err := buildLocalPkgOnHost(context.Background(), nil, "/src", EmitOpts{DryRun: true}); err == nil {
		t.Error("buildLocalPkgOnHost(nil) should error")
	}
	// Empty build template → error (config missing build_template).
	empty := &LocalPkgDef{PkgGlob: "*.pkg.tar.zst"}
	if _, err := buildLocalPkgOnHost(context.Background(), empty, "/src", EmitOpts{DryRun: true}); err == nil {
		t.Error("empty build_template should error")
	}
}

// TestBuildDepPkgsOnHost_EmptyAndDryRun proves the no-op contracts of the
// aur-CANDY dep-build helper: empty packages → (nil, nil) with no build; DryRun →
// (nil, nil) logging the plan; an empty builder image (or nil builder def) with
// packages → error (never a silent drop).
func TestBuildDepPkgsOnHost_EmptyAndDryRun(t *testing.T) {
	lp := testPacLocalPkgDef()
	_, bc, _, err := LoadBuildConfigForBox(repoRootDir(t))
	if err != nil {
		t.Fatalf("LoadBuildConfigForBox: %v", err)
	}
	aurDef := bc.Builder["aur"]
	if aurDef == nil {
		t.Fatal("aur builder not defined in build.yml")
	}
	// Empty packages: pure no-op regardless of builder/dryrun — never shells out.
	if pkgs, err := buildDepPkgsOnHost(context.Background(), lp, aurDef, "", nil, "", nil, "", EmitOpts{}); err != nil || pkgs != nil {
		t.Errorf("empty packages = (%v, %v), want (nil, nil)", pkgs, err)
	}
	// DryRun with packages + builder + def: no build, no error.
	if pkgs, err := buildDepPkgsOnHost(context.Background(), lp, aurDef, "arch-builder:latest", []string{"cloudflared-bin"}, "", nil, "", EmitOpts{DryRun: true}); err != nil || pkgs != nil {
		t.Errorf("dry-run = (%v, %v), want (nil, nil)", pkgs, err)
	}
	// Packages but no builder image (live): hard error, never a silent drop.
	if _, err := buildDepPkgsOnHost(context.Background(), lp, aurDef, "", []string{"cloudflared-bin"}, "", nil, "", EmitOpts{}); err == nil {
		t.Error("buildDepPkgsOnHost with packages but no builder image should error")
	}
	// Packages + image but nil builder def: hard error.
	if _, err := buildDepPkgsOnHost(context.Background(), lp, nil, "arch-builder:latest", []string{"cloudflared-bin"}, "", nil, "", EmitOpts{}); err == nil {
		t.Error("buildDepPkgsOnHost with nil builder def should error")
	}
}

// TestLocalPkgDef_RoundTripFromBuildYML proves the pac/rpm/deb formats in the
// repo's build.yml carry a complete local_pkg block this code reads — guarding
// the config-driven contract end to end. Loads the real build.yml.
func TestLocalPkgDef_RoundTripFromBuildYML(t *testing.T) {
	dc, _, _, err := LoadBuildConfigForBox(repoRootDir(t))
	if err != nil {
		t.Fatalf("LoadBuildConfigForBox: %v", err)
	}
	check := func(distro, format string, wantDepBuilder bool) {
		d := dc.ResolveDistro([]string{distro})
		if d == nil {
			t.Fatalf("%s distro not found in build.yml", distro)
		}
		fmtName, lp := d.LocalPkgFormat(format)
		if fmtName != format || lp == nil {
			t.Fatalf("%s %s format has no local_pkg block: fmt=%q lp=%#v", distro, format, fmtName, lp)
		}
		if lp.PkgGlob == "" || lp.SourceSentinel == "" || lp.BuildTemplate == "" || lp.InstallTemplate == "" || lp.Probe == "" {
			t.Errorf("build.yml %s.%s.local_pkg is incomplete: %#v", distro, format, lp)
		}
		if wantDepBuilder && lp.DepBuilder == "" {
			t.Errorf("%s.%s.local_pkg should declare dep_builder (aur-layer path): %#v", distro, format, lp)
		}
	}
	check("arch", "pac", true)
	check("fedora", "rpm", false)
	check("debian", "deb", false)
	// cachyos inherits arch's pac format; ubuntu inherits debian's deb format.
	if cachy := dc.ResolveDistro([]string{"cachyos"}); cachy != nil {
		if _, clp := cachy.LocalPkgFormat("pac"); clp == nil {
			t.Error("cachyos (inherits arch) should resolve the pac local_pkg block")
		}
	}
	if ub := dc.ResolveDistro([]string{"ubuntu"}); ub != nil {
		if _, ulp := ub.LocalPkgFormat("deb"); ulp == nil {
			t.Error("ubuntu (inherits debian) should resolve the deb local_pkg block")
		}
	}
}

// repoRootDir walks up from the test's working dir to the directory containing
// build.yml (the project root), so the round-trip test finds the real config
// regardless of the package-test cwd.
func repoRootDir(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for range 16 {
		// The repo root holds the unified charly.yml entry point. build.yml is no
		// longer a reliable marker — it's embedded in the binary, and the charly/
		// source dir carries the embed-source build.yml.
		if _, err := os.Stat(filepath.Join(dir, UnifiedFileName)); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Skip("charly.yml not found walking up from test cwd; skipping round-trip")
	return ""
}

// renderLocalPkgImageInstall: a PRODUCTION box build DOWNLOADS the candy's
// PUBLISHED release package (latest released toolchain) and installs it via the
// shared install template — never a COPY of a locally-built package.
func TestRenderLocalPkgImageInstall_ProductionDownloadsRelease(t *testing.T) {
	lp := testPacLocalPkgDef()
	lp.DownloadTemplate = "https://github.com/opencharly/charly/releases/latest/download/opencharly-${ARCH}.pkg.tar.zst"
	s := &LocalPkgInstallStep{CandyName: "charly", Format: "pac", LocalPkg: lp}
	got, err := renderLocalPkgImageInstall(s, false, "", "")
	if err != nil {
		t.Fatalf("production render: %v", err)
	}
	if !strings.Contains(got, "curl -fsSL") || !strings.Contains(got, "releases/latest/download/opencharly-${ARCH}.pkg.tar.zst") {
		t.Errorf("production mode must DOWNLOAD the published release; got:\n%s", got)
	}
	if !strings.Contains(got, "pacman -U --noconfirm") {
		t.Errorf("production mode must install via the format install_template; got:\n%s", got)
	}
	if strings.Contains(got, "COPY ") {
		t.Errorf("production mode must NOT COPY a locally-built package; got:\n%s", got)
	}
}

// renderLocalPkgImageInstall: a DISPOSABLE check bed (devLocalPkg=true) builds the
// in-development package from LOCAL source. With no localpkg source dir present it
// HARD ERRORS — it must NEVER silently fall back to the published release (R4: no
// black-magic fallback that would let a bed test a stale binary).
func TestRenderLocalPkgImageInstall_DevMissingSourceHardErrors(t *testing.T) {
	lp := testPacLocalPkgDef()
	lp.DownloadTemplate = "https://example.com/opencharly-${ARCH}.pkg.tar.zst"
	s := &LocalPkgInstallStep{
		CandyName:   "charly",
		Format:      "pac",
		PkgbuildRef: "pkg/arch",
		CandyDir:    t.TempDir(), // no PKGBUILD sentinel here
		ProjectDir:  t.TempDir(), // nor here
		LocalPkg:    lp,
	}
	_, err := renderLocalPkgImageInstall(s, true, t.TempDir(), "charly-arch")
	if err == nil {
		t.Fatalf("dev-local-pkg with no source dir must HARD ERROR (no silent fallback to the release); got nil")
	}
	if !strings.Contains(err.Error(), "dev-local-pkg") {
		t.Errorf("dev-mode error should name dev-local-pkg; got: %v", err)
	}
}
