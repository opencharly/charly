package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/vmshared"
	"github.com/opencharly/spec/spec"
	"gopkg.in/yaml.v3"
)

// The pure localpkg-mechanism tests (ResolveLocalPkgDir, BuildLocalPkgOnHost,
// TransferAndInstallPkgs, VenueHasPkgManager, ExecLocalPkgInstall,
// RenderLocalPkgImageInstall) moved to sdk/deploykit/localpkg_test.go (W3) —
// they exercise ONLY deploykit.CandyModel/buildkit.ResolvedBox-adjacent SDK
// types, no *Config/registry. What stays HERE needs the loader (LoadBuildConfigForBox),
// the live *Candy concrete type, or a core-only entry point (ociEmitStep).

// TestCompileLocalPkgStep / TestBuildDeployPlanLocalPkgOrdering relocated to
// candy/plugin-bundle (#55 decoupling, Batch A) — they asserted
// deploykit.CompileLocalPkgStep/BuildDeployPlan directly, zero charly dep. testPacDistroDef
// (which wrapped testPacLocalPkgDef below into a *spec.ResolvedDistro) moved with them — it had
// no other charly consumer. testPacLocalPkgDef got its OWN port there too (a
// plugin-bundle-local copy, since this out-of-module package can't share unexported test
// helpers with charly) — the copy here STAYS because charly's own build_target_oci_test.go
// (outside this batch) also depends on it directly.

// testPacLocalPkgDef returns a vmshared.LocalPkgDef mirroring build.yml's `pac.local_pkg`
// block — the config that drives the localpkg mechanism. Tests use it so they
// exercise the SAME config-driven path the loader produces, without parsing YAML.
func testPacLocalPkgDef() *vmshared.LocalPkgDef {
	return &vmshared.LocalPkgDef{
		PkgGlob:         "*.pkg.tar.zst",
		SourceSentinel:  "PKGBUILD",
		BuildTemplate:   "cd {{.SrcDir}} && PKGDEST={{.PkgDest}} makepkg -sf --noconfirm",
		InstallTemplate: "pacman -U --noconfirm {{.StageDir}}/{{.Glob}}",
		Probe:           "command -v pacman",
		DepBuilder:      "aur",
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
// candy/plugin-installstep OpEmit → deploykit.RenderLocalPkgImageInstall, called directly — a
// pure function of the step + the BuildEnv scalars, no project structure needed), which returns
// "" for a nil LocalPkg — so ociEmitStep succeeds and returns nothing.
func TestOCITargetLocalPkgNilContractEmitsNothing(t *testing.T) {
	step := &spec.LocalPkgInstallStep{PkgbuildRef: "pkg/arch", CandyName: "charly"}
	frag, err := ociEmitStep(step, &spec.InstallPlan{}, nil, buildEngineContext{})
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

// TestBuildDepPkgsOnHost_EmptyAndDryRun relocated to candy/plugin-bundle (#55 decoupling,
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
