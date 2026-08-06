package main

import (
	"reflect"
	"testing"

	"github.com/opencharly/spec/spec"
)

// TestCascade_FormatFamilyLevel / TestCascade_UnionAndTopBase / TestCascade_MostSpecificRepoWins
// / TestCascade_DeterministicRepoPerDistro / TestCascade_FedoraArchBareReach /
// TestCascade_TopOnlyCandyInstallsEverywhere (+ their private debImg/pkgStep/fmtImg helpers)
// relocated to candy/plugin-bundle (#55 decoupling, Batch A) — they asserted
// deploykit.CompileSystemPackageSteps/CascadeTagChain directly, zero charly dep.

// --- Parser routing --------------------------------------------------------
//
// TestCascade_BareDistroRoutesToTagSection / TestCascade_VersionedAndCompoundKeys /
// TestCascade_ArchAurStaysFormatSection / TestCascade_TopPackagesNotFoldedAtParse (+ their
// shared deriveCandy helper) relocated to candy/plugin-loader (#55 decoupling; Batch A executed
// this move on Batch C's behalf per the cross-batch file-ownership matrix) — they exercised
// loaderkit.ScanInlineCandy's own routing directly, zero charly dep.

// --- Cascade resolution ---------------------------------------------------
//
// TestCascade_UnionAndTopBase / TestCascade_MostSpecificRepoWins /
// TestCascade_DeterministicRepoPerDistro / TestCascade_FedoraArchBareReach /
// TestCascade_TopOnlyCandyInstallsEverywhere relocated to candy/plugin-bundle (#55
// decoupling, Batch A) — see the note at the top of this file.

// --- distroTagChain -------------------------------------------------------

func TestDistroTagChain(t *testing.T) {
	cases := []struct {
		distro, version string
		want            []string
	}{
		{"ubuntu", "24.04", []string{"ubuntu:24.04", "ubuntu"}},
		{"debian", "13", []string{"debian:13", "debian"}},
		{"arch", "", []string{"arch"}}, // rolling — bare only
		{"", "", nil},
	}
	for _, c := range cases {
		if got := spec.DistroTagChain(c.distro, c.version); !reflect.DeepEqual(got, c.want) {
			t.Errorf("distroTagChain(%q,%q) = %v, want %v", c.distro, c.version, got, c.want)
		}
	}
}

func TestDistroDefVersionInherits(t *testing.T) {
	dc := &spec.DistroConfig{Distro: map[string]*spec.ResolvedDistro{
		"debian": {Version: "13", Bootstrap: spec.Bootstrap{InstallCmd: "apt"}},
		"ubuntu": {Inherits: "debian", Version: "24.04", Bootstrap: spec.Bootstrap{InstallCmd: "apt"}},
		"cachy":  {Inherits: "debian", Bootstrap: spec.Bootstrap{InstallCmd: "apt"}}, // no own version
	}}
	if v := dc.ResolveInherits(dc.Distro["ubuntu"], 10).Version; v != "24.04" {
		t.Errorf("ubuntu version = %q, want 24.04 (child wins)", v)
	}
	if v := dc.ResolveInherits(dc.Distro["cachy"], 10).Version; v != "13" {
		t.Errorf("cachy version = %q, want inherited 13", v)
	}
}

// --- Package-cascade inheritance (cachyos pulls arch; ubuntu does NOT) -----

// TestExpandPackageInheritance proves the YAML-driven asymmetry: a distro with
// inherit_packages: true expands its cascade chain to include the inherits:
// ancestor (cachyos → [cachyos, arch]) so an `arch:` candy block reaches it,
// while a distro that only sets inherits: (ubuntu → debian) does NOT pull the
// parent's package sections. No Go-side hardcoded inheritance table.
func TestExpandPackageInheritance(t *testing.T) {
	dc := &spec.DistroConfig{Distro: map[string]*spec.ResolvedDistro{
		"arch":    {Format: map[string]*spec.Format{"pac": {}, "aur": {Secondary: true}}},
		"cachyos": {Inherits: "arch", InheritPackages: true},
		"debian":  {Format: map[string]*spec.Format{"deb": {}}},
		"ubuntu":  {Inherits: "debian"}, // format inheritance only
		"fedora":  {Format: map[string]*spec.Format{"rpm": {}}},
		// transitive opt-in: a grandchild flagged on each hop walks the whole chain
		"cachyos-edge": {Inherits: "cachyos", InheritPackages: true},
	}}
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"cachyos pulls arch", []string{"cachyos"}, []string{"cachyos", "arch"}},
		{"arch unchanged", []string{"arch"}, []string{"arch"}},
		{"ubuntu does NOT pull debian", []string{"ubuntu"}, []string{"ubuntu"}},
		{"debian unchanged", []string{"debian"}, []string{"debian"}},
		{"fedora unchanged", []string{"fedora"}, []string{"fedora"}},
		{"idempotent when ancestor authored", []string{"cachyos", "arch"}, []string{"cachyos", "arch"}},
		{"versioned bare-name matched", []string{"cachyos:rolling", "cachyos"}, []string{"cachyos:rolling", "cachyos", "arch"}},
		{"transitive multi-hop", []string{"cachyos-edge"}, []string{"cachyos-edge", "cachyos", "arch"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := dc.ExpandPackageInheritance(tc.in); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ExpandPackageInheritance(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
	// nil config returns input unchanged (no panic).
	if got := (*spec.DistroConfig)(nil).ExpandPackageInheritance([]string{"cachyos"}); !reflect.DeepEqual(got, []string{"cachyos"}) {
		t.Errorf("nil dc must return input unchanged, got %v", got)
	}
}

// --- Legacy-shape rejection (no migration; hard error) --------------------

// TestRejectLegacyTopLevelFormatAndDistroKeys proves the candy-manifest guard
// hard-errors on a package-format key or a per-distro tag section placed at the
// candy root (they nest under `distro:`). The vocabulary is the DYNAMIC build
// vocabulary registered from build.yml — no hardcoded format/distro list, and no
// migration: these shapes are simply invalid.
func TestRejectLegacyTopLevelFormatAndDistroKeys(t *testing.T) {
	RegisterBuildVocabulary(testDistroConfig())
	cases := []struct {
		key  string
		want bool
	}{
		// Vocabulary comes from testdata/build.yml: distros arch/debian/fedora/
		// ubuntu, formats pac/aur/deb/rpm.
		{"pac", true}, {"deb", true}, {"rpm", true}, {"aur", true},
		{"debian", true}, {"debian:13", true}, {"debian,ubuntu", true},
		{"arch", true}, {"fedora", true},
		{"package", false}, {"distro", false}, {"service", false},
		{"task", false}, {"description", false}, {"", false},
		{"cachyos", true}, // now provided by the embedded default build vocabulary
	}
	for _, tc := range cases {
		if got := spec.NewCandyVocab(testDistroConfig()).LooksLikeDistroOrFormatKey(tc.key); got != tc.want {
			t.Errorf("CandyVocab.LooksLikeDistroOrFormatKey(%q) = %v, want %v", tc.key, got, tc.want)
		}
	}
}
