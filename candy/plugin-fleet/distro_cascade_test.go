package fleet

import (
	"reflect"
	"testing"

	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/sdk/vmshared"
	"github.com/opencharly/spec/spec"

	"gopkg.in/yaml.v3"
)

// distro_cascade_test.go — relocated (in part) from charly/distro_cascade_test.go (#55
// decoupling, Batch A): the Cascade-RESOLUTION tests — these assert deploykit.
// CompileSystemPackageSteps/CascadeTagChain directly, zero charly dep. The parser-routing
// tests (TestCascade_BareDistroRoutesToTagSection/_VersionedAndCompoundKeys/
// _ArchAurStaysFormatSection/_TopPackagesNotFoldedAtParse) are Batch C's concern
// (plugin-loader, exercising loaderkit.ScanInlineCandy's own routing) and stay in charly, as do
// TestDistroTagChain/TestDistroDefVersionInherits/TestExpandPackageInheritance/
// TestRejectLegacyTopLevelFormatAndDistroKeys (zero kit dep).
//
// deriveCandy here is a PLUGIN-FLEET-LOCAL port of charly's helper: charly's version decodes
// through requireProjectLoader().DecodeEntityViaCUE (the loader's CUE-validating decode,
// sdk/loaderkit-backed since K1 unit 1); this file instead uses plain yaml.Unmarshal +
// normalizePackageShorthand (the same narrow package-shorthand canonicalization loadRealCandy
// uses) — sufficient for every fixture body these 6 tests author (plain package lists + map-form
// repo blocks, no other CUE-only shorthand), with no host-seam dependency. charly's OWN
// deriveCandy stays untouched (Batch C's parser-routing tests, which remain in charly, still need
// it).
func deriveCandy(t *testing.T, body string) spec.CandyReader {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("parse: %v", err)
	}
	normalizePackageShorthand(&doc)
	var ly spec.CandyYAML
	if err := doc.Decode(&ly); err != nil {
		t.Fatalf("decode: %v", err)
	}
	m, v, _ := loaderkit.ScanInlineCandy("t", "", &ly)
	return testCandy("t", m, v)
}

// debImg builds a minimal ResolvedBox with a deb primary format and the given
// most-specific-first distro tag chain.
func debImg(chain ...string) *buildkit.ResolvedBox {
	return &buildkit.ResolvedBox{ResolvedBox: spec.ResolvedBox{Pkg: "deb", Distro: chain}, DistroDef: &spec.ResolvedDistro{Format: map[string]*vmshared.FormatDef{"deb": {}}}}
}

func pkgStep(t *testing.T, steps []spec.InstallStep) *spec.SystemPackagesStep {
	t.Helper()
	var found *spec.SystemPackagesStep
	n := 0
	for _, s := range steps {
		if sp, ok := s.(*spec.SystemPackagesStep); ok {
			found = sp
			n++
		}
	}
	if n != 1 {
		t.Fatalf("expected exactly 1 SystemPackagesStep, got %d", n)
	}
	return found
}

// fmtImg builds a minimal ResolvedBox with the given primary package format and
// most-specific-first distro tag chain.
func fmtImg(format string, chain ...string) *buildkit.ResolvedBox {
	return &buildkit.ResolvedBox{ResolvedBox: spec.ResolvedBox{Pkg: format, Distro: chain}, DistroDef: &spec.ResolvedDistro{Format: map[string]*vmshared.FormatDef{format: {}}}}
}

// TestCascade_FormatFamilyLevel proves the package-format FAMILY level
// (`distro: deb:`/`pac:`/`rpm:`) applies to every distro of that format, while
// distro-specific blocks stay scoped. This is the YAML-configured
// deb/pac/rpm → distro → version hierarchy: a candy declares family-generic
// packages ONCE under the format tag instead of duplicating per distro, and a
// `pac:` block reaches arch AND cachyos with no Go-side distro inheritance.
func TestCascade_FormatFamilyLevel(t *testing.T) {
	// deb family: shared under `deb:`, debian-only under `debian:`.
	debCandy := deriveCandy(t, "name: t\ndistro:\n  deb:\n    package: [shared]\n  debian:\n    package: [deb-only]\n")
	debian := pkgStep(t, deploykit.CompileSystemPackageSteps(debCandy, fmtImg("deb", "debian:13", "debian"), deploykit.HostContext{})).Packages
	ubuntu := pkgStep(t, deploykit.CompileSystemPackageSteps(debCandy, fmtImg("deb", "ubuntu:24.04", "ubuntu"), deploykit.HostContext{})).Packages
	if !reflect.DeepEqual(debian, []string{"shared", "deb-only"}) {
		t.Errorf("debian = %v, want [shared deb-only]", debian)
	}
	if !reflect.DeepEqual(ubuntu, []string{"shared"}) {
		t.Errorf("ubuntu = %v, want [shared] ONLY — deb-only must NOT leak from the debian block", ubuntu)
	}

	// pac family: a single `pac:` block reaches BOTH arch and cachyos.
	pacCandy := deriveCandy(t, "name: t\ndistro:\n  pac:\n    package: [sddm]\n")
	arch := pkgStep(t, deploykit.CompileSystemPackageSteps(pacCandy, fmtImg("pac", "arch"), deploykit.HostContext{})).Packages
	cachyos := pkgStep(t, deploykit.CompileSystemPackageSteps(pacCandy, fmtImg("pac", "cachyos"), deploykit.HostContext{})).Packages
	if !reflect.DeepEqual(arch, []string{"sddm"}) || !reflect.DeepEqual(cachyos, []string{"sddm"}) {
		t.Errorf("pac family: arch=%v cachyos=%v, want both [sddm]", arch, cachyos)
	}

	// cascadeTagChain order: distro chain, then format tag (least-specific) last.
	if got := deploykit.CascadeTagChain(fmtImg("pac", "cachyos")); !reflect.DeepEqual(got, []string{"cachyos", "pac"}) {
		t.Errorf("cascadeTagChain = %v, want [cachyos pac]", got)
	}
}

func TestCascade_UnionAndTopBase(t *testing.T) {
	l := deriveCandy(t, `
name: t
package: [base]
distro:
  ubuntu:
    package: [u]
  ubuntu-24.04:
    package: [u2404]
`)
	step := pkgStep(t, deploykit.CompileSystemPackageSteps(l, debImg("ubuntu:24.04", "ubuntu"), deploykit.HostContext{}))
	// base (top-level, first) ∪ ubuntu ∪ ubuntu:24.04, deduped.
	if !reflect.DeepEqual(step.Packages, []string{"base", "u", "u2404"}) {
		t.Errorf("packages = %v, want [base u u2404]", step.Packages)
	}
}

func TestCascade_MostSpecificRepoWins(t *testing.T) {
	l := deriveCandy(t, `
name: t
distro:
  ubuntu:
    package: [pkg]
    repo: [{name: r, suite: from-bare}]
  ubuntu-24.04:
    repo: [{name: r, suite: from-version}]
`)
	step := pkgStep(t, deploykit.CompileSystemPackageSteps(l, debImg("ubuntu:24.04", "ubuntu"), deploykit.HostContext{}))
	repos := buildkit.ToMapSlice(step.RawInstallContext["repo"])
	if len(repos) != 1 || repos[0]["suite"] != "from-version" {
		t.Errorf("most-specific repo must win: got %v, want suite=from-version", repos)
	}
}

// TestCascade_DeterministicRepoPerDistro is the regression guard for the
// ORIGINAL bug: debian and ubuntu both declaring a repo used to collapse into
// one mutable "deb" format section whose winner depended on Go's randomized map
// iteration. With per-distro tag sections + sorted derive, the SAME repo
// resolves every time regardless of authoring/map order.
func TestCascade_DeterministicRepoPerDistro(t *testing.T) {
	body := `
name: t
distro:
  debian:
    package: [tailscale]
    repo: [{name: tailscale, suite: trixie}]
  ubuntu:
    package: [tailscale]
    repo: [{name: tailscale, suite: noble}]
`
	for i := range 50 { // many iterations to defeat any map-order flakiness
		l := deriveCandy(t, body)
		deb := pkgStep(t, deploykit.CompileSystemPackageSteps(l, debImg("debian:13", "debian"), deploykit.HostContext{}))
		ubu := pkgStep(t, deploykit.CompileSystemPackageSteps(l, debImg("ubuntu:24.04", "ubuntu"), deploykit.HostContext{}))
		if s := buildkit.ToMapSlice(deb.RawInstallContext["repo"]); len(s) != 1 || s[0]["suite"] != "trixie" {
			t.Fatalf("iter %d: debian must resolve trixie, got %v", i, s)
		}
		if s := buildkit.ToMapSlice(ubu.RawInstallContext["repo"]); len(s) != 1 || s[0]["suite"] != "noble" {
			t.Fatalf("iter %d: ubuntu must resolve noble, got %v", i, s)
		}
	}
}

func TestCascade_FedoraArchBareReach(t *testing.T) {
	// A bare fedora image ([fedora]) must reach the fedora tag section — there is
	// no format-section fallback anymore.
	l := deriveCandy(t, `
name: t
distro:
  fedora:
    package: [vim]
`)
	img := &buildkit.ResolvedBox{ResolvedBox: spec.ResolvedBox{Pkg: "rpm", Distro: []string{"fedora"}}, DistroDef: &spec.ResolvedDistro{Format: map[string]*vmshared.FormatDef{"rpm": {}}}}
	step := pkgStep(t, deploykit.CompileSystemPackageSteps(l, img, deploykit.HostContext{}))
	if !reflect.DeepEqual(step.Packages, []string{"vim"}) {
		t.Errorf("fedora bare reach: packages = %v, want [vim]", step.Packages)
	}
}

func TestCascade_TopOnlyCandyInstallsEverywhere(t *testing.T) {
	// A candy with only a top-level package: (no distro:) installs that base on
	// any image via the primary format.
	l := deriveCandy(t, "name: t\npackage: [nodejs, npm]\n")
	step := pkgStep(t, deploykit.CompileSystemPackageSteps(l, debImg("debian:13", "debian"), deploykit.HostContext{}))
	if !reflect.DeepEqual(step.Packages, []string{"nodejs", "npm"}) {
		t.Errorf("top-only base: packages = %v, want [nodejs npm]", step.Packages)
	}
}
