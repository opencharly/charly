package main

// packaging_test.go — the charly candy's `packaging:` section
// (candy/charly/charly.yml) is the single source of truth for native-package
// metadata: `charly generate-packages` (sdk/packagekit) reads ONLY it, and the
// per-distro package repos pass it to the plugin as --candy. The three legacy
// pkg/* files (arch PKGBUILD, fedora spec, debian control) stay as test fixtures
// until the Phase 3 removal, so this file asserts (a) the section parses into the
// spec.Packaging type with every entry a plain package name, and (b) completeness —
// a dependency added to one of the legacy files but not the section fails CI.

import (
	"bufio"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/opencharly/spec/spec"
	"gopkg.in/yaml.v3"
)

const (
	candyCharlyYML = "../candy/charly/charly.yml"
	archPKGBUILD   = "../pkg/arch/PKGBUILD"
	fedoraSpec     = "../pkg/fedora/opencharly.spec"
	debianControl  = "../pkg/debian/debian/control"
	hostPlugins    = "../scripts/host-command-plugins.txt"
)

var plainName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9+._-]*$`)

// loadPackaging parses the `packaging:` section from the charly candy.
func loadPackaging(t *testing.T) *spec.Packaging {
	t.Helper()
	data, err := os.ReadFile(candyCharlyYML)
	if err != nil {
		t.Fatalf("read %s: %v", candyCharlyYML, err)
	}
	var doc struct {
		Charly struct {
			Candy struct {
				Packaging *spec.Packaging `yaml:"packaging"`
			} `yaml:"candy"`
		} `yaml:"charly"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse %s: %v", candyCharlyYML, err)
	}
	if doc.Charly.Candy.Packaging == nil {
		t.Fatalf("%s has no packaging: section", candyCharlyYML)
	}
	return doc.Charly.Candy.Packaging
}

// TestPackagingSectionWellFormed — the section parses into spec.Packaging, carries
// the common fields, and every dep/optdep/suggest/variant-plugin entry is a plain
// package name (no version operators, spaces, or commas).
func TestPackagingSectionWellFormed(t *testing.T) {
	pkg := loadPackaging(t)

	if pkg.Name != "charly" {
		t.Errorf("packaging.name = %q, want %q", pkg.Name, "charly")
	}
	if pkg.Description == "" {
		t.Error("packaging.description is empty")
	}
	if pkg.Maintainer == "" {
		t.Error("packaging.maintainer is empty")
	}

	for fname, f := range pkg.Formats {
		checkPlainNames(t, fname+".depends", f.Depends...)
		checkPlainNames(t, fname+".recommends", f.Recommends...)
		checkPlainNames(t, fname+".suggests", f.Suggests...)
		checkPlainNames(t, fname+".optdepends", optdepNames(f)...)
	}

	for vname, v := range pkg.Variants {
		if v.Description == "" {
			t.Errorf("variant %q has an empty description", vname)
		}
		checkPlainNames(t, "variant "+vname+" plugins", v.Plugins...)
	}
}

func checkPlainNames(t *testing.T, what string, names ...string) {
	t.Helper()
	for _, n := range names {
		if !plainName.MatchString(n) {
			t.Errorf("%s: %q is not a plain package name", what, n)
		}
	}
}

// TestPackagingSectionCompleteness — every dependency declared in the three legacy
// pkg/* files must be present in the packaging section. A dep added to a PKGBUILD/
// spec/control but not here fails CI; the section is the single source of truth the
// generate-packages plugin reads. (The reverse — a dep here but not in a legacy
// file — is NOT an error: deb-family deliberately omits tailscale, and the legacy
// files are fixtures scheduled for removal.)
func TestPackagingSectionCompleteness(t *testing.T) {
	pkg := loadPackaging(t)

	arch := pkg.Formats["archlinux"]
	rpm := pkg.Formats["rpm"]
	deb := pkg.Formats["deb"]
	if arch == nil || rpm == nil || deb == nil {
		t.Fatal("packaging.formats must have archlinux, rpm, and deb entries")
	}

	requireSubset(t, "archlinux.depends vs PKGBUILD depends", parsePkgbuildArray(t, archPKGBUILD, "depends"), arch.Depends)
	requireSubset(t, "archlinux.optdepends vs PKGBUILD optdepends", parsePkgbuildOptdepNames(t, archPKGBUILD), optdepNames(arch))
	requireSubset(t, "rpm.depends vs spec Requires", parseSpecList(t, fedoraSpec, "Requires"), rpm.Depends)
	requireSubset(t, "rpm.suggests vs spec Suggests", parseSpecList(t, fedoraSpec, "Suggests"), rpm.Suggests)
	requireSubset(t, "deb.depends vs control Depends", parseControlList(t, debianControl, "Depends"), deb.Depends)
	requireSubset(t, "deb.suggests vs control Suggests", parseControlList(t, debianControl, "Suggests"), deb.Suggests)

	// Every variant plugin must be one of the 9 welded plugins the release workflow
	// publishes, and the union of all variant plugin sets must cover every one of them.
	welded := readWeldedPlugins(t)
	union := map[string]bool{}
	for _, v := range pkg.Variants {
		for _, p := range v.Plugins {
			union[p] = true
			if !welded[p] {
				t.Errorf("variant plugin %q is not in scripts/host-command-plugins.txt", p)
			}
		}
	}
	for w := range welded {
		if !union[w] {
			t.Errorf("welded plugin %q appears in no packaging variant", w)
		}
	}
}

// requireSubset fails if any name in want is absent from have.
func requireSubset(t *testing.T, what string, want, have []string) {
	t.Helper()
	haveSet := make(map[string]bool, len(have))
	for _, h := range have {
		haveSet[h] = true
	}
	for _, w := range want {
		if !haveSet[w] {
			t.Errorf("%s: %q is missing from the packaging section", what, w)
		}
	}
}

// optdepNames returns the sorted keys of a format's optdepends map.
func optdepNames(f *spec.PackagingFormat) []string {
	if f == nil {
		return nil
	}
	out := make([]string, 0, len(f.OptDepends))
	for k := range f.OptDepends {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// parsePkgbuildArray extracts the single-quoted entries of a PKGBUILD array
// (`name=(\n 'a'\n 'b'\n)`), skipping comment lines. Entry lines carry inline
// comments with apostrophes (e.g. "exec'd"), so each line is parsed only up to its
// closing quote rather than with a span-matching regex.
func parsePkgbuildArray(t *testing.T, path, name string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	re := regexp.MustCompile(`(?ms)^` + regexp.QuoteMeta(name) + `=\(\n(.*?)\n\)`)
	m := re.FindStringSubmatch(string(data))
	if m == nil {
		t.Fatalf("%s: no ^%s=(…) array", path, name)
	}
	var out []string
	for _, line := range strings.Split(m[1], "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "'") {
			continue
		}
		end := strings.Index(line[1:], "'")
		if end < 0 {
			continue
		}
		out = append(out, line[1:1+end])
	}
	return out
}

// parsePkgbuildOptdepNames returns the package-name part (before the colon) of each
// `'name: description'` entry in a PKGBUILD optdepends array.
func parsePkgbuildOptdepNames(t *testing.T, path string) []string {
	names := parsePkgbuildArray(t, path, "optdepends")
	for i, n := range names {
		if j := strings.Index(n, ":"); j >= 0 {
			names[i] = strings.TrimSpace(n[:j])
		}
	}
	return names
}

// parseSpecList extracts the package names from `Name: value` lines of a fedora
// spec (one package per line, e.g. `Requires: glibc`).
func parseSpecList(t *testing.T, path, keyword string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	re := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(keyword) + `:\s+(\S+)\s*$`)
	matches := re.FindAllStringSubmatch(string(data), -1)
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, m[1])
	}
	return out
}

// parseControlList extracts the comma-separated package names from a debian control
// field that may span continuation lines (leading-space lines). Debian substitution
// variables (${...}) are dropped.
func parseControlList(t *testing.T, path, keyword string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	var value strings.Builder
	collecting := false
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, keyword+":") {
			collecting = true
			value.WriteString(strings.TrimPrefix(line, keyword+":"))
			continue
		}
		if collecting && !strings.HasPrefix(line, " ") {
			break // the next field begins
		}
		if collecting {
			value.WriteString(line)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan %s: %v", path, err)
	}

	var out []string
	for _, part := range strings.Split(value.String(), ",") {
		p := strings.TrimSpace(part)
		if p == "" || strings.HasPrefix(p, "${") {
			continue
		}
		out = append(out, p)
	}
	return out
}

// readWeldedPlugins returns the set of plugin names in
// scripts/host-command-plugins.txt (the welded command plugins the release workflow
// publishes).
func readWeldedPlugins(t *testing.T) map[string]bool {
	t.Helper()
	data, err := os.ReadFile(hostPlugins)
	if err != nil {
		t.Fatalf("read %s: %v", hostPlugins, err)
	}
	out := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out[line] = true
	}
	return out
}
