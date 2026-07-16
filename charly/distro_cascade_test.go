package main

import (
	"reflect"
	"testing"

	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
	"github.com/opencharly/sdk/vmshared"

	"gopkg.in/yaml.v3"
)

// deriveCandy parses a candy YAML body and runs the Calamares bridge, returning
// the populated Candy (tagSections / formatSections / topPackages). It decodes
// through the same CUE path the loader uses (normalize shorthand → CUE Decode),
// so shorthand bodies (bare-string packages, scalar ports) work without the
// deleted custom unmarshalers.
func deriveCandy(t *testing.T, body string) *Candy {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("parse: %v", err)
	}
	root := kit.MappingRoot(&doc)
	if root == nil {
		t.Fatalf("test candy body is not a mapping")
	}
	var ly spec.CandyYAML
	if err := decodeEntityViaCUE(root, reflect.TypeOf(spec.CandyYAML{}), &ly, "test-candy"); err != nil {
		t.Fatalf("decode: %v", err)
	}
	layer := &Candy{Name: "t"}
	derivePackageSectionsFromCalamares(layer, &ly)
	return layer
}

// debImg builds a minimal ResolvedBox with a deb primary format and the given
// most-specific-first distro tag chain.
func debImg(chain ...string) *buildkit.ResolvedBox {
	return &buildkit.ResolvedBox{
		Pkg:       "deb",
		Distro:    chain,
		DistroDef: &spec.ResolvedDistro{Format: map[string]*FormatDef{"deb": {}}},
	}
}

func pkgStep(t *testing.T, steps []spec.InstallStep) *deploykit.SystemPackagesStep {
	t.Helper()
	var found *deploykit.SystemPackagesStep
	n := 0
	for _, s := range steps {
		if sp, ok := s.(*deploykit.SystemPackagesStep); ok {
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
	return &buildkit.ResolvedBox{
		Pkg:       format,
		Distro:    chain,
		DistroDef: &spec.ResolvedDistro{Format: map[string]*FormatDef{format: {}}},
	}
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

// --- Parser routing -------------------------------------------------------

func TestCascade_BareDistroRoutesToTagSection(t *testing.T) {
	l := deriveCandy(t, `
name: t
distro:
  debian:
    package: [foo]
  ubuntu:
    package: [bar]
`)
	// Bare distro keys land in per-distro TAG sections, NOT a shared "deb" format
	// section (the collapse that caused the non-deterministic repo bug).
	if l.FormatSection("deb") != nil {
		t.Error("bare distro keys must NOT create a deb format section")
	}
	if d := l.TagSection("debian"); d == nil || !reflect.DeepEqual(d.Package, []string{"foo"}) {
		t.Errorf("TagSection(debian).Package = %v, want [foo]", d)
	}
	if u := l.TagSection("ubuntu"); u == nil || !reflect.DeepEqual(u.Package, []string{"bar"}) {
		t.Errorf("TagSection(ubuntu).Package = %v, want [bar]", u)
	}
}

func TestCascade_VersionedAndCompoundKeys(t *testing.T) {
	l := deriveCandy(t, `
name: t
distro:
  debian-13:
    package: [v13]
  "debian,ubuntu":
    package: [shared]
`)
	if d := l.TagSection("debian:13"); d == nil || d.Package[0] != "v13" {
		t.Errorf("debian-13 must route to tag debian:13, got %v", d)
	}
	// Compound splits into one tag section per distro, sharing the content.
	for _, tag := range []string{"debian", "ubuntu"} {
		c := l.TagSection(tag)
		if c == nil || !reflect.DeepEqual(c.Package, []string{"shared"}) {
			t.Errorf("compound tag %q = %v, want [shared]", tag, c)
		}
	}
}

func TestCascade_ArchAurStaysFormatSection(t *testing.T) {
	l := deriveCandy(t, `
name: t
distro:
  arch:
    package: [base]
    aur:
      package: [aur-pkg]
`)
	if a := l.TagSection("arch"); a == nil || a.Package[0] != "base" {
		t.Errorf("arch base must be a tag section, got %v", a)
	}
	// aur is a real build format — it keeps its dedicated format section.
	if aur := l.FormatSection("aur"); aur == nil || aur.Packages[0] != "aur-pkg" {
		t.Errorf("arch.aur must stay a format section, got %v", aur)
	}
}

func TestCascade_TopPackagesNotFoldedAtParse(t *testing.T) {
	l := deriveCandy(t, `
name: t
package: [base-pkg]
distro:
  debian:
    package: [deb-pkg]
`)
	// The top-level base is recorded separately and folded at RESOLVE time —
	// folding at parse is what cross-contaminated debian/ubuntu.
	if !reflect.DeepEqual(l.TopPackages(), []string{"base-pkg"}) {
		t.Errorf("TopPackages() = %v, want [base-pkg]", l.TopPackages())
	}
	if d := l.TagSection("debian"); d == nil || reflect.DeepEqual(d.Package, []string{"base-pkg", "deb-pkg"}) {
		t.Errorf("debian tag must NOT contain the top-level base at parse time, got %v", d.Package)
	}
}

// --- Cascade resolution ---------------------------------------------------

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

// TestCascade_DeterministicUnderShuffledMap is the regression guard for the
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
	img := &buildkit.ResolvedBox{Pkg: "rpm", Distro: []string{"fedora"},
		DistroDef: &spec.ResolvedDistro{Format: map[string]*FormatDef{"rpm": {}}}}
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
		if got := buildkit.DistroTagChain(c.distro, c.version); !reflect.DeepEqual(got, c.want) {
			t.Errorf("distroTagChain(%q,%q) = %v, want %v", c.distro, c.version, got, c.want)
		}
	}
}

func TestDistroDefVersionInherits(t *testing.T) {
	dc := &buildkit.DistroConfig{Distro: map[string]*spec.ResolvedDistro{
		"debian": {Version: "13", Bootstrap: vmshared.BootstrapDef{InstallCmd: "apt"}},
		"ubuntu": {Inherits: "debian", Version: "24.04", Bootstrap: vmshared.BootstrapDef{InstallCmd: "apt"}},
		"cachy":  {Inherits: "debian", Bootstrap: vmshared.BootstrapDef{InstallCmd: "apt"}}, // no own version
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
	dc := &buildkit.DistroConfig{Distro: map[string]*spec.ResolvedDistro{
		"arch":    {Format: map[string]*FormatDef{"pac": {}, "aur": {Secondary: true}}},
		"cachyos": {Inherits: "arch", InheritPackages: true},
		"debian":  {Format: map[string]*FormatDef{"deb": {}}},
		"ubuntu":  {Inherits: "debian"}, // format inheritance only
		"fedora":  {Format: map[string]*FormatDef{"rpm": {}}},
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
	if got := (*buildkit.DistroConfig)(nil).ExpandPackageInheritance([]string{"cachyos"}); !reflect.DeepEqual(got, []string{"cachyos"}) {
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
		if got := looksLikeDistroOrFormatKey(tc.key); got != tc.want {
			t.Errorf("looksLikeDistroOrFormatKey(%q) = %v, want %v", tc.key, got, tc.want)
		}
	}
}
