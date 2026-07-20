package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
	"gopkg.in/yaml.v3"
)

// The pure localpkg-mechanism tests (ResolveLocalPkgDir, BuildLocalPkgOnHost,
// TransferAndInstallPkgs, VenueHasPkgManager, ExecLocalPkgInstall,
// RenderLocalPkgImageInstall) moved to sdk/deploykit/localpkg_test.go (W3) —
// they exercise ONLY deploykit.CandyModel/buildkit.ResolvedBox-adjacent SDK
// types, no *Config/registry. What stays HERE needs the loader (LoadBuildConfigForBox),
// the live *Candy concrete type, or a core-only entry point (ociEmitStep).

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
func testPacDistroDef() *spec.ResolvedDistro {
	return &spec.ResolvedDistro{
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
	img := &buildkit.ResolvedBox{
		Name:      "charly-host",
		Pkg:       "pac",
		DistroDef: testPacDistroDef(),
		Builder:   map[string]string{"aur": "ghcr.io/opencharly/arch-builder:latest"},
	}
	hostCtx := deploykit.HostContext{MachineVenue: true, Distro: "arch"}

	// A candy with no localpkg entry for the target format → nil.
	if step := deploykit.CompileLocalPkgStep(testCandy("no-pkg", spec.CandyModel{}, spec.CandyView{}), img, hostCtx); step != nil {
		t.Errorf("candy with no localpkg: should compile to nil, got %T", step)
	}

	// The charly candy's per-format map: pac resolves to pkg/arch.
	l := testCandy("charly", spec.CandyModel{
		SourceDir: "/layers/charly",
		LocalPkg:  map[string]string{"pac": "pkg/arch", "rpm": "pkg/fedora", "deb": "pkg/debian"},
	}, spec.CandyView{})
	step := deploykit.CompileLocalPkgStep(l, img, hostCtx)
	if step == nil {
		t.Fatal("compileLocalPkgStep returned nil for a candy with a pac localpkg source")
	}
	pkg, ok := step.(*deploykit.LocalPkgInstallStep)
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
	rpmImg := &buildkit.ResolvedBox{Name: "charly-fedora", Pkg: "rpm", DistroDef: &spec.ResolvedDistro{Format: map[string]*FormatDef{
		"rpm": {LocalPkg: &LocalPkgDef{PkgGlob: "*.rpm", SourceSentinel: "*.spec", BuildTemplate: "x", InstallTemplate: "dnf install -y {{.StageDir}}/{{.Glob}}", Probe: "command -v dnf"}},
	}}}
	if rs, ok := deploykit.CompileLocalPkgStep(l, rpmImg, hostCtx).(*deploykit.LocalPkgInstallStep); !ok || rs.Format != "rpm" || rs.PkgbuildRef != "pkg/fedora" {
		t.Errorf("rpm distro should pick pkg/fedora via the format map, got %#v", deploykit.CompileLocalPkgStep(l, rpmImg, hostCtx))
	}

	// Distro with a format but NO localpkg block → nil (no native package).
	noFmt := deploykit.CompileLocalPkgStep(l, &buildkit.ResolvedBox{Name: "charly-x", Pkg: "rpm", DistroDef: &spec.ResolvedDistro{Format: map[string]*FormatDef{"rpm": {}}}}, hostCtx)
	if noFmt != nil {
		t.Errorf("distro without a localpkg-capable format should compile to nil, got %#v", noFmt)
	}
}

// TestLocalPkgInstallStepIR exercises the IR contract: kind, scope (system),
// venue (host-native), gate (none), reverse (no ledger ops — like apk).
func TestLocalPkgInstallStepIR(t *testing.T) {
	s := &deploykit.LocalPkgInstallStep{PkgbuildRef: "pkg/arch", CandyName: "charly"}
	if s.Kind() != spec.StepKindLocalPkgInstall {
		t.Errorf("Kind() = %q, want %q", s.Kind(), spec.StepKindLocalPkgInstall)
	}
	if s.Scope() != spec.ScopeSystem {
		t.Errorf("Scope() = %v, want ScopeSystem", s.Scope())
	}
	if s.Venue() != spec.VenueHostNative {
		t.Errorf("Venue() = %v, want VenueHostNative", s.Venue())
	}
	if s.RequiresGate() != spec.GateNone {
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
	l := testCandy("charly", spec.CandyModel{
		LocalPkg: map[string]string{"pac": "pkg/arch"},
		Plan: []spec.Step{
			{Run: "build", Op: spec.Op{Plugin: "command", PluginInput: map[string]any{"command": "echo install charly"}, RunAs: "root"}},
		},
	}, spec.CandyView{})
	img := &buildkit.ResolvedBox{Name: "host-adhoc", Home: "/root", User: "root", Pkg: "pac", DistroDef: testPacDistroDef()}
	plan, err := deploykit.BuildDeployPlan(l, img, deploykit.HostContext{MachineVenue: true, Distro: "arch"})
	if err != nil {
		t.Fatalf("BuildDeployPlan: %v", err)
	}
	pkgIdx, taskIdx := -1, -1
	for i, step := range plan.Steps {
		switch step.(type) {
		case *deploykit.LocalPkgInstallStep:
			if pkgIdx < 0 {
				pkgIdx = i
			}
		case *deploykit.OpStep:
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
// contract (LocalPkg==nil — a distro with no localpkg-capable format) renders nothing at image
// build. Post-C1.4 the build-emit routes through the FULL plugin chain (ociEmitStep →
// pluginEmitStepWords[LocalPkgInstall]="local-pkg-install" → spliceClassStepEmit →
// candy/plugin-installstep OpEmit → emitViaHostBuild → HostBuild("step-emit") →
// stepEmitLocalPkgInstall → deploykit.RenderLocalPkgImageInstall), which returns "" for a nil
// LocalPkg — so ociEmitStep succeeds and returns nothing.
func TestOCITargetLocalPkgNilContractEmitsNothing(t *testing.T) {
	step := &deploykit.LocalPkgInstallStep{PkgbuildRef: "pkg/arch", CandyName: "charly"}
	frag, err := ociEmitStep(step, &deploykit.InstallPlan{}, nil, buildEngineContext{})
	if err != nil {
		t.Fatalf("ociEmitStep(LocalPkgInstallStep, nil LocalPkg) = %v, want nil", err)
	}
	if frag != "" {
		t.Errorf("ociEmitStep emitted %q for a nil-LocalPkg localpkg step; should emit nothing", frag)
	}
}

// TestLocalPkgMapRejectsScalar proves the candy-manifest localpkg: field is CUE-CLOSED to the
// per-format map shape (schema/candy.cue: `localpkg?: {pac?: string, rpm?: string, deb?:
// string}`) — a legacy scalar form is rejected at CUE decode time (struct vs string type
// mismatch), and the per-format map decodes into CandyYAML.LocalPkg. The rejection moved from a
// hand-written LocalPkgMap.UnmarshalYAML (deleted with *Candy) to the schema itself (SDD): the
// decode path is the SAME decodeEntityViaCUE every candy manifest goes through.
func TestLocalPkgMapRejectsScalar(t *testing.T) {
	decode := func(body string) (spec.CandyYAML, error) {
		var doc yaml.Node
		if err := yaml.Unmarshal([]byte(body), &doc); err != nil {
			t.Fatalf("parse: %v", err)
		}
		root := kit.MappingRoot(&doc)
		if root == nil {
			t.Fatalf("test candy body is not a mapping")
		}
		var ly spec.CandyYAML
		err := decodeEntityViaCUE(root, reflect.TypeOf(spec.CandyYAML{}), &ly, "test-candy")
		return ly, err
	}

	if _, err := decode("name: t\nlocalpkg: pkg/arch\n"); err == nil {
		t.Error("scalar localpkg: should be rejected by CUE (per-format map shape), got nil error")
	}

	ly, err := decode("name: t\nlocalpkg:\n  pac: pkg/arch\n  rpm: pkg/fedora\n")
	if err != nil {
		t.Fatalf("map form should decode, got %v", err)
	}
	if ly.LocalPkg["pac"] != "pkg/arch" || ly.LocalPkg["rpm"] != "pkg/fedora" {
		t.Errorf("decoded map = %v", ly.LocalPkg)
	}
}

// TestBuildDepPkgsOnHost_EmptyAndDryRun proves the no-op contracts of the
// aur-CANDY dep-build helper (now deploykit.BuildDepPkgsOnHost): empty packages →
// (nil, nil) with no build; DryRun → (nil, nil) logging the plan; an empty builder
// image (or nil builder def) with packages → error (never a silent drop). Stays in
// charly because it needs LoadBuildConfigForBox (the loader) to fetch a REAL aur
// BuilderDef — the image-resolve/ensure closures are nil here since none of these
// cases actually invoke them (empty/dry-run/missing-image all short-circuit first).
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
	if pkgs, err := deploykit.BuildDepPkgsOnHost(context.Background(), lp, aurDef, "", nil, "", nil, nil, deploykit.EmitOpts{}); err != nil || pkgs != nil {
		t.Errorf("empty packages = (%v, %v), want (nil, nil)", pkgs, err)
	}
	// DryRun with packages + builder + def: no build, no error.
	if pkgs, err := deploykit.BuildDepPkgsOnHost(context.Background(), lp, aurDef, "arch-builder:latest", []string{"cloudflared-bin"}, "", nil, nil, deploykit.EmitOpts{DryRun: true}); err != nil || pkgs != nil {
		t.Errorf("dry-run = (%v, %v), want (nil, nil)", pkgs, err)
	}
	// Packages but no builder image (live): hard error, never a silent drop.
	if _, err := deploykit.BuildDepPkgsOnHost(context.Background(), lp, aurDef, "", []string{"cloudflared-bin"}, "", nil, nil, deploykit.EmitOpts{}); err == nil {
		t.Error("BuildDepPkgsOnHost with packages but no builder image should error")
	}
	// Packages + image but nil builder def: hard error.
	if _, err := deploykit.BuildDepPkgsOnHost(context.Background(), lp, nil, "arch-builder:latest", []string{"cloudflared-bin"}, "", nil, nil, deploykit.EmitOpts{}); err == nil {
		t.Error("BuildDepPkgsOnHost with nil builder def should error")
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
