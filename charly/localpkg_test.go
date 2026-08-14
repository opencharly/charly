package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/opencharly/spec/spec"
)

// The pure localpkg-mechanism tests (ResolveLocalPkgDir, BuildLocalPkgOnHost,
// TransferAndInstallPkgs, VenueHasPkgManager, ExecLocalPkgInstall,
// RenderLocalPkgImageInstall) moved to sdk/deploykit/localpkg_test.go (W3) —
// they exercise ONLY deploykit.CandyModel/buildkit.ResolvedBox-adjacent SDK
// types, no *Config/registry. What stays HERE needs the loader (LoadBuildConfigForBox),
// the live *Candy concrete type, or a core-only entry point (ociEmitStep).

// TestCompileLocalPkgStep / TestBuildDeployPlanLocalPkgOrdering relocated to
// candy/plugin-fleet (#55 decoupling, Batch A) — they asserted
// deploykit.CompileLocalPkgStep/BuildDeployPlan directly, zero charly dep. testPacDistroDef
// (which wrapped testPacLocalPkgDef below into a *spec.ResolvedDistro) moved with them — it had
// no other charly consumer. testPacLocalPkgDef got its OWN port there too (a
// plugin-fleet-local copy, since this out-of-module package can't share unexported test
// helpers with charly) — the copy here STAYS because charly's own build_target_oci_test.go
// (outside this batch) also depends on it directly.

// testPacLocalPkgDef returns a spec.LocalPkg mirroring build.yml's `pac.local_pkg`
// block — the config that drives the localpkg mechanism. Tests use it so they
// exercise the SAME config-driven path the loader produces, without parsing YAML.
// The source-build fields (pkg_glob/source_sentinel/build_template/dep_builder)
// are GONE (Phase 0a — the plugin builds now); only the install/download/probe
// machinery remains.
func testPacLocalPkgDef() *spec.LocalPkg {
	return &spec.LocalPkg{
		InstallTemplate: "pacman -U --noconfirm {{.StageDir}}/{{.Glob}}",
		Probe:           "command -v pacman",
		DownloadTemplate: "https://opencharly.github.io/charly-arch/${ARCH}/charly-${ARCH}.pkg.tar.zst",
	}
}

// TestLocalPkgInstallStepIR relocated to spec/spec/install_step_vocab_test.go
// (K3 cone2 test closure): pure IR-contract assertion on spec.LocalPkgInstallStep
// (Kind/Scope/Venue/RequiresGate/Reverse), zero charly-core dependency.

// TestOCITargetLocalPkgNilContractEmitsNothing proves a localpkg step with NO LocalPkg
// contract (LocalPkg==nil — a distro with no localpkg-capable format) renders nothing at image
// build. The build-emit routes through the FULL plugin chain (ociEmitStep → dispatchOCIStep →
// candy/plugin-installstep's "oci-dispatch" → pluginEmitStepWords[LocalPkgInstall]="local-pkg-install" →
// InvokeProvider("step","local-pkg-install") →
// candy/plugin-installstep ops.OpEmit → deploykit.RenderLocalPkgImageInstall, called directly — a
// pure function of the step + the BuildEnv scalars, no project structure needed), which returns
// "" for a nil LocalPkg — so ociEmitStep succeeds and returns nothing.
func TestOCITargetLocalPkgNilContractEmitsNothing(t *testing.T) {
	step := &spec.LocalPkgInstallStep{CandyName: "charly"}
	frag, err := ociEmitStep(step, &spec.InstallPlan{}, nil, buildEngineContext{})
	if err != nil {
		t.Fatalf("ociEmitStep(LocalPkgInstallStep, nil LocalPkg) = %v, want nil", err)
	}
	if frag != "" {
		t.Errorf("ociEmitStep emitted %q for a nil-LocalPkg localpkg step; should emit nothing", frag)
	}
}

// TestLocalPkgMapRejectsScalar (the candy-manifest localpkg: field is REMOVED — nFPM cutover —
// a candy carrying it is a hard schema violation) relocated to sdk/loaderkit/decode_entity_test.go
// (K1 unit 1) — it exercises ONLY spec.CandyYAML + loaderkit.DecodeEntityViaCUE, zero charly-core
// dependency.

// TestBuildDepPkgsOnHost_EmptyAndDryRun relocated to candy/plugin-fleet (#55 decoupling,
// Batch A; fixture-reworked to a synthetic aur builder def, since every asserted case
// short-circuits before reading the def's fields) — it asserted deploykit.BuildDepPkgsOnHost
// directly.

// TestLocalPkgDef_RoundTripFromBuildYML proves the pac/rpm/deb formats in the
// repo's build.yml carry a complete local_pkg block this code reads — guarding
// the config-driven contract end to end. Loads the real build.yml.
func TestLocalPkgDef_RoundTripFromBuildYML(t *testing.T) {
	dc, _, _, err := LoadBuildConfigForBox(repoRootDir(t))
	if err != nil {
		t.Fatalf("LoadBuildConfigForBox: %v", err)
	}
	check := func(distro, format string) {
		d := dc.ResolveDistro([]string{distro})
		if d == nil {
			t.Fatalf("%s distro not found in build.yml", distro)
		}
		fmtName, lp := d.LocalPkgFormat(format)
		if fmtName != format || lp == nil {
			t.Fatalf("%s %s format has no local_pkg block: fmt=%q lp=%#v", distro, format, fmtName, lp)
		}
		if lp.InstallTemplate == "" || lp.Probe == "" || lp.DownloadTemplate == "" {
			t.Errorf("build.yml %s.%s.local_pkg is incomplete: %#v", distro, format, lp)
		}
	}
	check("arch", "pac")
	check("fedora", "rpm")
	check("debian", "deb")
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
		if _, err := os.Stat(filepath.Join(dir, spec.UnifiedFileName)); err == nil {
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
