package main

// packaging_test.go — the charly candy's `packaging:` section
// (packaging/charly.yml) is the single source of truth for native-package
// metadata: `charly generate-packages` (sdk/packagekit) reads ONLY it, and the
// per-distro package repos pass it to the plugin as --candy. The three legacy
// pkg/* files (arch PKGBUILD, fedora spec, debian control) were removed with the
// nFPM cutover, so this file asserts (a) the section parses into the
// spec.Packaging type with every entry a plain package name, (b) the variant
// plugin sets are exactly the welded plugins the release workflow publishes
// (now 11, incl. plugin-review), and (c) the systemd: unit + config: sections the
// package ships (the systemd-started charly MCP server's units and its
// system-wide /etc/charly/charly.yml project).

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/opencharly/spec/spec"
	"gopkg.in/yaml.v3"
)

const (
	candyCharlyYML = "../packaging/charly.yml"
	// charly-dev is declared in the PROJECT manifest, not a candy dir: its
	// `copy: bin/charly` must resolve against the repo root, and a candy takes its
	// declaring file's directory as SourceDir.
	rootCharlyYML = "../charly.yml"
	hostPlugins   = "../scripts/host-command-plugins.txt"
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

// TestPackagingVariantsCoverWeldedPlugins — every variant plugin must be one of
// the welded plugins the release workflow publishes, and the union of all
// variant plugin sets must cover every one of them. A variant naming a plugin
// absent from the release tarball fails loudly at package-build time (the plugin
// validates the variant's list against the --plugins dir); this test catches the
// drift at the source.
func TestPackagingVariantsCoverWeldedPlugins(t *testing.T) {
	pkg := loadPackaging(t)

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

// TestPackagingSystemdDeclarations — the packaging.systemd section ships two
// non-autostarting charly-mcp units (system + user scope), both binding the MCP
// server to loopback only on the HOST (P3 — --listen 127.0.0.1:18765; a
// pod/VM deployment overrides with --listen 0.0.0.0:18765) and both running
// from the shipped system project (/etc/charly). sdk/packagekit renders them to
// /usr/lib/systemd/{system,user}/<name>.service; the per-distro install tests
// assert the units exist and are disabled (the non-autostarting contract — the
// operator starts on demand with systemctl start / systemctl --user start).
func TestPackagingSystemdDeclarations(t *testing.T) {
	pkg := loadPackaging(t)
	if len(pkg.Systemd) != 2 {
		t.Fatalf("packaging.systemd has %d unit(s), want 2 (system + user charly-mcp)", len(pkg.Systemd))
	}
	byScope := map[string]*spec.PackagingSystemdUnit{}
	for _, u := range pkg.Systemd {
		if u.Name != "charly-mcp" {
			t.Errorf("unit name = %q, want %q", u.Name, "charly-mcp")
		}
		if !strings.Contains(u.Exec, "127.0.0.1:18765") {
			t.Errorf("unit %q exec %q does not bind 127.0.0.1:18765 (the host loopback default)", u.Scope, u.Exec)
		}
		if u.Restart != "on-failure" {
			t.Errorf("unit %q restart = %q, want on-failure (an on-demand unit: a clean stop stays stopped)", u.Scope, u.Restart)
		}
		if u.Working_directory != "/etc/charly" {
			t.Errorf("unit %q working_directory = %q, want /etc/charly (the shipped system project)", u.Scope, u.Working_directory)
		}
		byScope[u.Scope] = u
	}
	if byScope["system"] == nil {
		t.Error("no system-scope unit (rendered to /usr/lib/systemd/system/charly-mcp.service)")
	}
	if byScope["user"] == nil {
		t.Error("no user-scope unit (rendered to /usr/lib/systemd/user/charly-mcp.service)")
	}
	sys := byScope["system"]
	if len(sys.After) == 0 || !slices.Contains(sys.After, "network-online.target") {
		t.Errorf("system unit after = %v, want network-online.target", sys.After)
	}
	if len(sys.Wants) == 0 || !slices.Contains(sys.Wants, "network-online.target") {
		t.Errorf("system unit wants = %v, want network-online.target", sys.Wants)
	}
}

// TestPackagingConfigDeclared — the packaging.config section ships a system-wide
// project charly.yml (/etc/charly/charly.yml) carrying the plugin candy ref the
// systemd-started MCP server needs (plugin-mcp), so the server resolves a local
// project (via WorkingDirectory=/etc/charly) instead of falling back to a network
// fetch of opencharly/charly. sdk/packagekit renders it into the package; the
// per-distro install tests assert the box-validate passes on the installed file
// (the shipped system project is a valid charly.yml at the declared schema version).
func TestPackagingConfigDeclared(t *testing.T) {
	pkg := loadPackaging(t)
	cfg := pkg.Config
	if cfg == nil {
		t.Fatal("packaging.config is missing")
	}
	if cfg.Path != "/etc/charly/charly.yml" {
		t.Errorf("config.path = %q, want /etc/charly/charly.yml", cfg.Path)
	}
	if cfg.Version == "" {
		t.Error("config.version is empty (must be the packaged charly's schema version — what charly migrate would produce)")
	}
	if cfg.Description == "" {
		t.Error("config.description is empty")
	}
	if len(cfg.Plugins) == 0 {
		t.Fatal("config.plugins is empty (the systemd MCP server would have no plugin source)")
	}
	if !strings.Contains(cfg.Plugins[0], "plugin-mcp") {
		t.Errorf("config.plugins[0] = %q, want a plugin-mcp candy ref", cfg.Plugins[0])
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
		// Entries are `<name>@<tag>`: the release workflow clones each welded plugin's
		// own repo at that pin. A bare name is a malformed entry, not a legacy form —
		// the workflow rejects it too, so accepting it here would let the test pass on
		// a list the release cannot build.
		name, ref, ok := strings.Cut(line, "@")
		if !ok || name == "" || ref == "" {
			t.Errorf("%s: %q is not `<name>@<tag>`", hostPlugins, line)
			continue
		}
		out[name] = true
	}
	return out
}

// TestDistroRepoInstallDeclared — the charly candy's `distro:` section must
// declare the charly package + a signed repo entry for every distro the
// release workflow publishes (debian, ubuntu, fedora, arch, alpine). The
// distro repo install is the canonical binary source for box compositions
// (the baked `copy: bin/charly` step was removed with the repo-install
// cutover); a box composing the candy without these entries would have no
// charly binary. Fails without the distro-repo change.
func TestDistroRepoInstallDeclared(t *testing.T) {
	data, err := os.ReadFile(candyCharlyYML)
	if err != nil {
		t.Fatalf("read %s: %v", candyCharlyYML, err)
	}
	var doc struct {
		Charly struct {
			Candy struct {
				Distro map[string]struct {
					Package []string         `yaml:"package"`
					Repo    []map[string]any `yaml:"repo"`
				} `yaml:"distro"`
			} `yaml:"candy"`
		} `yaml:"charly"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse %s: %v", candyCharlyYML, err)
	}
	distros := doc.Charly.Candy.Distro
	for _, d := range []string{"debian", "ubuntu", "fedora", "arch", "alpine"} {
		cfg, ok := distros[d]
		if !ok {
			t.Errorf("distro %q: no distro: section entry", d)
			continue
		}
		if !slices.Contains(cfg.Package, "charly") {
			t.Errorf("distro %q: package: list %v does not include %q", d, cfg.Package, "charly")
		}
		if len(cfg.Repo) == 0 {
			t.Errorf("distro %q: no repo: entry (the charly binary has no install source)", d)
		}
	}
}

// TestCharlyDevCandyDeclared — the candy split contract: `charly` installs from the
// published per-distro package repos (no copy step), and `charly-dev` is the
// local-source install for charly check beds ONLY (the sole surviving `copy:
// bin/charly` step, resolved relative to the REPO ROOT — charly-dev is declared in
// charly.yml, and a candy's SourceDir is its declaring file's dir). A regress here (e.g.
// the copy step leaking back into `charly`, or charly-dev losing its copy step)
// would silently reintroduce the stale-binary install that broke every
// remote-fetched `charly` candy (bin/charly is gitignored).
func TestCharlyDevCandyDeclared(t *testing.T) {
	dev, err := os.ReadFile(rootCharlyYML)
	if err != nil {
		t.Fatalf("read %s: %v", rootCharlyYML, err)
	}
	devDoc := struct {
		CharlyDev struct {
			Candy struct {
				Version string `yaml:"version"`
				Plan    []struct {
					Run  string `yaml:"run"`
					Copy string `yaml:"copy"`
					To   string `yaml:"to"`
				} `yaml:"plan"`
			} `yaml:"candy"`
		} `yaml:"charly-dev"`
	}{}
	if err := yaml.Unmarshal(dev, &devDoc); err != nil {
		t.Fatalf("parse %s: %v", rootCharlyYML, err)
	}
	devC := devDoc.CharlyDev.Candy
	foundCopy := false
	for _, step := range devC.Plan {
		if step.Run == "copy=bin/charly" && step.Copy == "bin/charly" && step.To == "/usr/bin/charly" {
			foundCopy = true
		}
	}
	if !foundCopy {
		t.Errorf("charly-dev: plan has no `run: copy=bin/charly` / `copy: bin/charly` / `to: /usr/bin/charly` step")
	}

	// The `charly` candy must NOT carry the copy step — its binary comes from the
	// published package repos (TestDistroRepoInstallDeclared asserts those exist).
	mainData, err := os.ReadFile(candyCharlyYML)
	if err != nil {
		t.Fatalf("read %s: %v", candyCharlyYML, err)
	}
	mainDoc := struct {
		Charly struct {
			Candy struct {
				Plan []struct {
					Run  string `yaml:"run"`
					Copy string `yaml:"copy"`
				} `yaml:"plan"`
			} `yaml:"candy"`
		} `yaml:"charly"`
	}{}
	if err := yaml.Unmarshal(mainData, &mainDoc); err != nil {
		t.Fatalf("parse %s: %v", candyCharlyYML, err)
	}
	for _, step := range mainDoc.Charly.Candy.Plan {
		if step.Copy != "" {
			t.Errorf("charly: plan must not carry a copy step (the package install is the binary source); found copy=%q", step.Copy)
		}
	}
}

// TestInlineCandySourceDirIsProjectRoot pins the rule that lets charly-dev live in
// charly.yml instead of a candy/ directory: a candy declared INLINE in the project
// manifest takes the manifest's own directory as its SourceDir
// (loader_threaded.go:395), so a relative `copy:` path resolves against the repo root.
//
// This is not a style detail. charly-dev's `copy: bin/charly` is the sole surviving
// local-source install, and repo-root bin/charly is where `task build:binary` writes
// the binary. If inline candies ever anchored somewhere else, that copy would silently
// read the wrong path — or nothing — and every check bed welding charly-dev would
// install a stale or absent binary while still validating. The former
// candy/charly-dev/bin/charly copy existed ONLY to satisfy a candy-dir-relative
// resolution; this test is what makes deleting it safe.
func TestInlineCandySourceDirIsProjectRoot(t *testing.T) {
	root := t.TempDir()
	manifest := "version: 2026.249.2125\n" +
		"inline-copy-candy:\n" +
		"    candy:\n" +
		"        version: 2026.249.2125\n" +
		"        description: |-\n" +
		"            Inline candy carrying a relative copy: path, the charly-dev shape.\n" +
		"        plan:\n" +
		"            - run: copy=bin/charly\n" +
		"              copy: bin/charly\n" +
		"              to: /usr/bin/charly\n" +
		"              mode: \"0755\"\n"
	if err := os.WriteFile(filepath.Join(root, spec.UnifiedFileName), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	uf, ok, err := LoadUnified(root)
	if err != nil || !ok || uf == nil {
		t.Fatalf("LoadUnified(%s): ok=%v err=%v", root, ok, err)
	}
	scanned, scanErr := ScanAllCandyWithConfig(root, uf.ProjectConfig())
	if scanErr != nil {
		t.Fatalf("scan failed: %v", scanErr)
	}
	dirs := candyDirsFromScan(scanned)

	src, found := dirs["inline-copy-candy"]
	if !found {
		t.Fatalf("inline candy absent from the scanned set (keys: %v)", candyDirKeys(dirs))
	}
	if src != root {
		t.Fatalf("inline candy SourceDir = %q, want the project root %q — a relative\n"+
			"`copy:` would resolve against the wrong directory, which is exactly the\n"+
			"breakage that deleting candy/charly-dev/bin/charly would then hide", src, root)
	}
}

// candyDirKeys returns a map's keys, for failure messages only.
func candyDirKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
