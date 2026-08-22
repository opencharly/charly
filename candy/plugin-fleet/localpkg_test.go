package fleet

import (
	"context"
	"testing"

	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/vmshared"
	"github.com/opencharly/spec/spec"
)

// localpkg_test.go — relocated (in part) from charly/localpkg_test.go (#55 decoupling, Batch
// A): TestCompileLocalPkgStep/TestBuildDeployPlanLocalPkgOrdering assert deploykit.
// CompileLocalPkgStep/BuildDeployPlan directly, zero charly dep. TestBuildDepPkgsOnHost_
// EmptyAndDryRun originally sourced a REAL aur builder def via charly's LoadBuildConfigForBox
// (the project loader) — none of its assertions actually inspect that def's fields (every case
// short-circuits on empty-packages/dry-run/missing-image/nil-def before touching it), so it is
// reworked here with a synthetic fixture def instead. TestLocalPkgInstallStepIR/
// TestOCITargetLocalPkgNilContractEmitsNothing (white-box ociEmitStep)/TestLocalPkgDef_
// RoundTripFromBuildYML (white-box LoadBuildConfigForBox) stay in charly.
// TestLocalPkgMapRejectsScalar (white-box decodeEntityViaCUE) moved on to
// sdk/loaderkit/decode_entity_test.go (K1 unit 1) — it exercises the relocated CUE-decode
// mechanism directly, zero charly-core dependency, so it no longer belongs in either place.

// testPacLocalPkgDef returns a vmshared.LocalPkgDef mirroring build.yml's `pac.local_pkg`
// block — the config that drives the localpkg mechanism. Tests use it so they
// exercise the SAME config-driven path the loader produces, without parsing YAML.
// The source-build fields (pkg_glob/source_sentinel/build_template/dep_builder)
// are GONE (Phase 0a — the plugin builds now); only the install/download/probe
// machinery remains, and the package glob is DERIVED from the download_template URL.
func testPacLocalPkgDef() *vmshared.LocalPkgDef {
	return &vmshared.LocalPkgDef{
		InstallTemplate:  "pacman -U --noconfirm {{.StageDir}}/{{.Glob}}",
		Probe:            "command -v pacman",
		DownloadTemplate: "https://opencharly.github.io/charly-arch/${ARCH}/charly-${ARCH}.pkg.tar.zst",
	}
}

// testPacDistroDef returns a DistroDef whose `pac` format carries the localpkg
// contract — so compileLocalPkgStep resolves it the way it would from build.yml.
func testPacDistroDef() *spec.ResolvedDistro {
	return &spec.ResolvedDistro{
		Format: map[string]*vmshared.FormatDef{
			"pac": {LocalPkg: testPacLocalPkgDef()},
		},
	}
}

// TestCompileLocalPkgStep verifies a candy's `packaging:` section + the target
// distro's per-format `local_pkg:` block compile into a single
// LocalPkgInstallStep carrying the published package name + the config-driven
// LocalPkg; a candy with no packaging section, or a distro with no
// localpkg-capable format, compiles to nothing.
func TestCompileLocalPkgStep(t *testing.T) {
	img := &buildkit.ResolvedBox{ResolvedBox: spec.ResolvedBox{Name: "charly-host", Pkg: "pac", Builder: map[string]string{"aur": "ghcr.io/opencharly/arch-builder:latest"}}, DistroDef: testPacDistroDef()}
	hostCtx := deploykit.HostContext{MachineVenue: true, Distro: "arch"}

	// A candy with no packaging section → nil (no published package to obtain).
	if step := deploykit.CompileLocalPkgStep(testCandy("no-pkg", spec.CandyModel{}, spec.CandyView{}), img, hostCtx); step != nil {
		t.Errorf("candy with no packaging: should compile to nil, got %T", step)
	}

	// The charly candy's packaging section: pac resolves the format's local_pkg block.
	l := testCandy("charly", spec.CandyModel{
		SourceDir: "/layers/charly",
		Packaging: &spec.Packaging{Name: "charly"},
	}, spec.CandyView{})
	step := deploykit.CompileLocalPkgStep(l, img, hostCtx)
	if step == nil {
		t.Fatal("compileLocalPkgStep returned nil for a candy with a packaging section")
	}
	pkg, ok := step.(*spec.LocalPkgInstallStep)
	if !ok {
		t.Fatalf("compileLocalPkgStep returned %T, want *LocalPkgInstallStep", step)
	}
	if pkg.PackageName != "charly" || pkg.CandyName != "charly" || pkg.CandyDir != "/layers/charly" {
		t.Errorf("step fields = %+v", pkg)
	}
	// Format + LocalPkg config resolved from the distro's pac format (config-driven).
	if pkg.Format != "pac" || pkg.LocalPkg == nil || pkg.LocalPkg.DownloadTemplate == "" {
		t.Errorf("LocalPkg config not resolved from the pac format: Format=%q LocalPkg=%#v", pkg.Format, pkg.LocalPkg)
	}

	// Same candy on an rpm distro → picks the rpm format's local_pkg block.
	rpmImg := &buildkit.ResolvedBox{ResolvedBox: spec.ResolvedBox{Name: "charly-fedora", Pkg: "rpm"}, DistroDef: &spec.ResolvedDistro{Format: map[string]*vmshared.FormatDef{
		"rpm": {LocalPkg: &vmshared.LocalPkgDef{InstallTemplate: "dnf install -y {{.StageDir}}/{{.Glob}}", Probe: "command -v dnf", DownloadTemplate: "https://opencharly.github.io/charly-fedora/${ARCH}/charly-${ARCH}.rpm"}},
	}}}
	if rs, ok := deploykit.CompileLocalPkgStep(l, rpmImg, hostCtx).(*spec.LocalPkgInstallStep); !ok || rs.Format != "rpm" || rs.PackageName != "charly" {
		t.Errorf("rpm distro should pick the rpm format's local_pkg block, got %#v", deploykit.CompileLocalPkgStep(l, rpmImg, hostCtx))
	}

	// Distro with a format but NO localpkg block → nil (no native package).
	noFmt := deploykit.CompileLocalPkgStep(l, &buildkit.ResolvedBox{ResolvedBox: spec.ResolvedBox{Name: "charly-x", Pkg: "rpm"}, DistroDef: &spec.ResolvedDistro{Format: map[string]*vmshared.FormatDef{"rpm": {}}}}, hostCtx)
	if noFmt != nil {
		t.Errorf("distro without a localpkg-capable format should compile to nil, got %#v", noFmt)
	}
}

// TestBuildDeployPlanLocalPkgOrdering proves the localpkg step is emitted BEFORE
// the candy's task steps in the compiled plan — load-bearing so the charly candy's
// package-aware cmd: gate sees charly already installed and does nothing
// (instead of curling a /usr/local/bin/charly that shadows /usr/bin/charly).
func TestBuildDeployPlanLocalPkgOrdering(t *testing.T) {
	l := testCandy("charly", spec.CandyModel{
		Packaging: &spec.Packaging{Name: "charly"},
		Plan: []spec.Step{
			{Run: "build", Op: spec.Op{Plugin: "command", PluginInput: map[string]any{"command": "echo install charly"}, RunAs: "root"}},
		},
	}, spec.CandyView{})
	img := &buildkit.ResolvedBox{ResolvedBox: spec.ResolvedBox{Name: "host-adhoc", Home: "/root", User: "root", Pkg: "pac"}, DistroDef: testPacDistroDef()}
	ctx, ex := testConstructStepExecutor()
	plan, err := deploykit.BuildDeployPlan(ctx, ex, l, img, deploykit.HostContext{MachineVenue: true, Distro: "arch"})
	if err != nil {
		t.Fatalf("BuildDeployPlan: %v", err)
	}
	pkgIdx, taskIdx := -1, -1
	for i, step := range plan.Steps {
		switch step.(type) {
		case *spec.LocalPkgInstallStep:
			if pkgIdx < 0 {
				pkgIdx = i
			}
		case *spec.OpStep:
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

// TestBuildDepPkgsOnHost_EmptyAndDryRun proves the no-op contracts of the
// aur-CANDY dep-build helper (deploykit.BuildDepPkgsOnHost): empty packages →
// (nil, nil) with no build; DryRun → (nil, nil) logging the plan; an empty builder
// image (or nil builder def) with packages → error (never a silent drop). Every case
// short-circuits before actually reading the builder def's fields, so a synthetic aur-shaped
// fixture def stands in for charly's real build.yml aur entry (which TestLocalPkgDef_
// RoundTripFromBuildYML, staying in charly, verifies directly).
func TestBuildDepPkgsOnHost_EmptyAndDryRun(t *testing.T) {
	lp := testPacLocalPkgDef()
	aurDef := &buildkit.BuilderDef{DetectConfig: "aur", Phases: &spec.PhaseSet{Install: &spec.PhaseTemplates{Container: "pacman -U --noconfirm {{.Glob}}"}}}
	// Empty packages: pure no-op regardless of builder/dryrun — never shells out.
	if pkgs, err := deploykit.BuildDepPkgsOnHost(context.Background(), lp, "", aurDef, "", nil, "", nil, nil, spec.EmitOpts{}); err != nil || pkgs != nil {
		t.Errorf("empty packages = (%v, %v), want (nil, nil)", pkgs, err)
	}
	// DryRun with packages + builder + def: no build, no error.
	if pkgs, err := deploykit.BuildDepPkgsOnHost(context.Background(), lp, "", aurDef, "arch-builder:latest", []string{"cloudflared-bin"}, "", nil, nil, spec.EmitOpts{DryRun: true}); err != nil || pkgs != nil {
		t.Errorf("dry-run = (%v, %v), want (nil, nil)", pkgs, err)
	}
	// Packages but no builder image (live): hard error, never a silent drop.
	if _, err := deploykit.BuildDepPkgsOnHost(context.Background(), lp, "", aurDef, "", []string{"cloudflared-bin"}, "", nil, nil, spec.EmitOpts{}); err == nil {
		t.Error("BuildDepPkgsOnHost with packages but no builder image should error")
	}
	// Packages + image but nil builder def: hard error.
	if _, err := deploykit.BuildDepPkgsOnHost(context.Background(), lp, "", nil, "arch-builder:latest", []string{"cloudflared-bin"}, "", nil, nil, spec.EmitOpts{}); err == nil {
		t.Error("BuildDepPkgsOnHost with nil builder def should error")
	}
}
