package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// helper — write a file under root, creating parent directories as needed
func writeFixture(t *testing.T, root, rel, body string) {
	t.Helper()
	abs := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadUnified_AbsentFileReturnsNotPresent(t *testing.T) {
	root := t.TempDir()
	uf, present, err := LoadUnified(root)
	if err != nil {
		t.Fatalf("LoadUnified: %v", err)
	}
	if present {
		t.Error("present = true, want false for empty dir")
	}
	if uf != nil {
		t.Error("uf != nil, want nil when file absent")
	}
}

func TestLoadUnified_BasicRoot(t *testing.T) {
	root := t.TempDir()
	// Compact node-form: an image entity is `<name>: {candy: <FULL BODY>}` with
	// every collection (distro/candy) INLINE in the kind value.
	writeFixture(t, root, "charly.yml", `version: 2026.203.2359
defaults:
  registry: quay.io/example
  build: [rpm]
fedora:
  candy:
    base: quay.io/fedora/fedora:43
    distro: [fedora:43, fedora]
    candy: [base]
`)
	uf, present, err := LoadUnified(root)
	if err != nil {
		t.Fatalf("LoadUnified: %v", err)
	}
	if !present {
		t.Fatal("present = false, want true")
	}
	if uf.Version != LatestSchemaVersion().String() {
		t.Errorf("Version = %q, want %q", uf.Version, LatestSchemaVersion().String())
	}
	if uf.Defaults.Registry != "quay.io/example" {
		t.Errorf("Defaults.Registry = %q, want quay.io/example", uf.Defaults.Registry)
	}
	fedora, ok := uf.BoxConfig("fedora")
	if !ok {
		t.Fatal("box.fedora missing")
	}
	if fedora.Base != "quay.io/fedora/fedora:43" {
		t.Errorf("Base = %q", fedora.Base)
	}
}

func TestLoadUnified_NewerSchemaRejectedWithUpdateHint(t *testing.T) {
	root := t.TempDir()
	// A version far past LatestSchemaVersion(): the binary is behind the
	// config, so migrating cannot help — the user must update charly.
	writeFixture(t, root, "charly.yml", `version: 9999.141.1530
box:
  fedora:
    base: quay.io/fedora/fedora:43
`)
	_, _, err := LoadUnified(root)
	if err == nil {
		t.Fatal("expected hard-fail for a config newer than the binary supports")
	}
	msg := err.Error()
	if !strings.Contains(msg, "newer than this charly supports") {
		t.Errorf("error %q missing 'newer than this charly supports'", msg)
	}
	if !strings.Contains(msg, "Update charly") {
		t.Errorf("error %q missing 'Update charly' advice", msg)
	}
	if strings.Contains(msg, "charly migrate") {
		t.Errorf("error %q wrongly advises 'charly migrate' for a too-new config", msg)
	}
}

func TestLoadUnified_IncludesMerge(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "charly.yml", `version: 2026.203.2359
import:
  - build.yml
  - images.yml
defaults:
  registry: override.example.com
`)
	writeFixture(t, root, "build.yml", `fedora:
  distro:
    bootstrap:
      install_cmd: "dnf install"
      package: [dnf5]
`)
	writeFixture(t, root, "images.yml", `defaults:
  registry: included.example.com
  build: [rpm]
fedora:
  candy:
    base: quay.io/fedora/fedora:43
`)
	uf, _, err := LoadUnified(root)
	if err != nil {
		t.Fatalf("LoadUnified: %v", err)
	}
	// Root-wins: charly.yml's registry must override includes.
	if uf.Defaults.Registry != "override.example.com" {
		t.Errorf("Registry = %q, want override.example.com (root wins)", uf.Defaults.Registry)
	}
	// Includes contribute fields not set in root.
	if len(uf.Defaults.Build) != 1 || uf.Defaults.Build[0] != "rpm" {
		t.Errorf("Defaults.Build = %v, want [rpm]", uf.Defaults.Build)
	}
	if uf.Distros()["fedora"] == nil {
		t.Error("Distros.fedora missing")
	}
	if _, ok := uf.Box["fedora"]; !ok {
		t.Error("Box.fedora missing")
	}
}

func TestLoadUnified_IncludeCycleDetected(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "charly.yml", `version: 2026.203.2359
import: [a.yml]
`)
	writeFixture(t, root, "a.yml", `import: [b.yml]
`)
	writeFixture(t, root, "b.yml", `import: [a.yml]
`)
	_, _, err := LoadUnified(root)
	if err == nil {
		t.Fatal("expected cycle error, got nil")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("error = %v, want message mentioning cycle", err)
	}
}

// TestLoadUnified_MultiDocumentNodeForm — the loader parses a flat-imported file
// as a YAML multi-document stream of NODE-FORM docs (`---`-separated). Each doc is
// an arbitrary-name entity node (`<name>: {<kind>: <FULL BODY>}`) with its
// collections (package/candy) INLINE in the kind value; all merge into the root
// maps. Legacy multi-document KIND-KEYED loading (`candy: {name: …}` per doc) is
// removed — that shape is now a hard-reject (see TestLoadUnified_LegacyKindKeyedRejected).
func TestLoadUnified_MultiDocumentNodeForm(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "charly.yml", `version: 2026.203.2359
import: [bundle.yml]
`)
	writeFixture(t, root, "bundle.yml", `chrome:
  candy:
    package: [chromium]
---
firefox:
  candy:
    package: [firefox]
---
browsers:
  candy:
    base: quay.io/fedora/fedora:43
    candy: [chrome, firefox]
`)
	uf, _, err := LoadUnified(root)
	if err != nil {
		t.Fatalf("LoadUnified: %v", err)
	}
	if _, ok := uf.Candy["chrome"]; !ok {
		t.Error("candy.chrome missing")
	}
	if _, ok := uf.Candy["firefox"]; !ok {
		t.Error("candy.firefox missing")
	}
	if _, ok := uf.Box["browsers"]; !ok {
		t.Error("box.browsers missing")
	}
}

// TestLoadUnified_LegacyKindKeyedRejected — node-form is the ONLY authoring
// surface. A legacy kind-keyed document (a top-level `candy:`/`box:`/… map of
// entities, or a single `<kind>: {name: …}`) is hard-rejected with a `charly
// migrate` hint. This replaced the former "ambiguous (root+kind) doc" classifier,
// now gone: the bilingual reader was deleted, so EVERY kind-keyed shape — the old
// "ambiguous" case (here, a doc carrying both `candy:` and `box:`) included —
// routes through this one rejection.
func TestLoadUnified_LegacyKindKeyedRejected(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "charly.yml", `version: 2026.203.2359
import: [bundle.yml]
`)
	writeFixture(t, root, "bundle.yml", `candy:
  name: broken
box:
  name: broken-too
`)
	_, _, err := LoadUnified(root)
	if err == nil {
		t.Fatal("expected hard-reject for a legacy kind-keyed doc, got nil")
	}
	// A legacy kind-keyed doc stamped at HEAD is hard-rejected by the node-form
	// parser (a real, below-HEAD legacy config is caught by the version gate first).
	if !strings.Contains(err.Error(), "no kind discriminator") {
		t.Errorf("error = %v, want a node-form 'no kind discriminator' rejection", err)
	}
	if !strings.Contains(err.Error(), "charly migrate") {
		t.Errorf("error = %v, want a `charly migrate` hint", err)
	}
}

func TestLoadUnified_DiscoverCandies(t *testing.T) {
	root := t.TempDir()
	// Node-form charly.yml manifests; discovery routes by shape, registering the
	// candy keyed by its directory base (chrome / firefox).
	writeFixture(t, root, "candy/chrome/charly.yml", `chrome:
  candy:
    version: 2026.001.0001
`)
	writeFixture(t, root, "candy/firefox/charly.yml", `firefox:
  candy:
    version: 2026.001.0001
`)
	writeFixture(t, root, "charly.yml", `version: 2026.203.2359
discover:
  - candy
`)
	uf, _, err := LoadUnified(root)
	if err != nil {
		t.Fatalf("LoadUnified: %v", err)
	}
	if err := uf.ApplyDiscover(root); err != nil {
		t.Fatalf("ApplyDiscover: %v", err)
	}
	if _, ok := uf.Candy["chrome"]; !ok {
		t.Error("discovered candy.chrome missing")
	}
	if _, ok := uf.Candy["firefox"]; !ok {
		t.Error("discovered candy.firefox missing")
	}
}

func TestLoadUnified_DiscoverExplicitWinsOverDiscovered(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "candy/chrome/charly.yml", `chrome:
  candy:
    version: 2026.001.0001
`)
	// Node-form: an explicit inline candy entry (`chrome: {candy: {…}}`) is
	// defined directly in charly.yml. It must win over the discovered
	// candy/chrome dir — discovery skips a name already present.
	writeFixture(t, root, "charly.yml", `version: 2026.203.2359
discover:
  - candy
chrome:
  candy:
    version: 2026.100.0001
`)
	uf, _, err := LoadUnified(root)
	if err != nil {
		t.Fatalf("LoadUnified: %v", err)
	}
	if err := uf.ApplyDiscover(root); err != nil {
		t.Fatalf("ApplyDiscover: %v", err)
	}
	il, ok := decodeInlineCandy(uf.Candy["chrome"])
	if !ok {
		t.Fatal("candy.chrome missing")
	}
	// The explicit inline entry won: it is defined IN-PLACE (From == ""), not a
	// lazy `From: candy/chrome` directory reference the discovery walk would have
	// registered, and its inline body (version 2026.100.0001) survived rather than
	// the discovered manifest's (2026.001.0001).
	if il.From != "" {
		t.Errorf("discovered dir clobbered the explicit entry: From = %q, want \"\" (inline)", il.From)
	}
	if il.Version != "2026.100.0001" {
		t.Errorf("explicit inline body lost: Version = %q, want 2026.100.0001", il.Version)
	}
}

func TestLoadUnified_ScanSpecStringShorthand(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "charly.yml", `version: 2026.203.2359
discover:
  - layers
  - { path: vendor, recursive: false }
`)
	uf, _, err := LoadUnified(root)
	if err != nil {
		t.Fatalf("LoadUnified: %v", err)
	}
	if len(uf.Discover) != 2 {
		t.Fatalf("Discover = %#v, want 2 entries", uf.Discover)
	}
	// Post-2026-05 "discover anchoring" cutover (commit 460fabb): scan
	// specs are anchored to the including file's directory at merge time
	// so a relative `- layers` entry inside an included charly.yml
	// resolves against THAT file's location, not the consumer's cwd. The
	// test fixture lives at <tempdir>/charly.yml, so the anchored path
	// is filepath.Join(<tempdir>, "layers").
	wantCandies := filepath.Join(root, "layers")
	wantVendor := filepath.Join(root, "vendor")
	if uf.Discover[0].Path != wantCandies || !uf.Discover[0].Recursive {
		t.Errorf("[0] = %+v, want {Path:%s Recursive:true}", uf.Discover[0], wantCandies)
	}
	if uf.Discover[1].Path != wantVendor || uf.Discover[1].Recursive {
		t.Errorf("[1] = %+v, want {Path:%s Recursive:false}", uf.Discover[1], wantVendor)
	}
	// The string shorthand and the object form both default Manifest to the
	// single unified filename (configurable per spec via `manifest:` in charly.yml).
	if uf.Discover[0].Manifest != UnifiedFileName || uf.Discover[1].Manifest != UnifiedFileName {
		t.Errorf("Manifest defaults = %q,%q, want %q", uf.Discover[0].Manifest, uf.Discover[1].Manifest, UnifiedFileName)
	}
}

// TestLoadUnified_DiscoverConfigurableManifest proves the per-directory manifest
// filename is fully configured in charly.yml — discovery is told to look for
// a CUSTOM manifest name under a CUSTOM path, with zero per-kind filename baked
// into the loader.
func TestLoadUnified_DiscoverConfigurableManifest(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "stuff/widget/thing.yml", `widget:
  candy:
    version: 2026.001.0001
`)
	writeFixture(t, root, "charly.yml", `version: 2026.203.2359
discover:
  - { path: stuff, recursive: true, manifest: thing.yml }
`)
	uf, _, err := LoadUnified(root)
	if err != nil {
		t.Fatalf("LoadUnified: %v", err)
	}
	if err := uf.ApplyDiscover(root); err != nil {
		t.Fatalf("ApplyDiscover: %v", err)
	}
	if _, ok := uf.Candy["widget"]; !ok {
		t.Error("configurable-manifest discovery did not find widget under manifest: thing.yml")
	}
}

// TestLoadUnified_DiscoverRoutesNonCandyByShape proves a discovered manifest is
// routed by SHAPE, not by a per-kind filename: a `box:`-shaped manifest found by
// a generic discover spec merges as a box, not a candy.
func TestLoadUnified_DiscoverRoutesNonCandyByShape(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "entities/myimg/entity.yml", `myimg:
  candy:
    base: quay.io/fedora/fedora:43
`)
	writeFixture(t, root, "charly.yml", `version: 2026.203.2359
discover:
  - { path: entities, recursive: true, manifest: entity.yml }
`)
	uf, _, err := LoadUnified(root)
	if err != nil {
		t.Fatalf("LoadUnified: %v", err)
	}
	if err := uf.ApplyDiscover(root); err != nil {
		t.Fatalf("ApplyDiscover: %v", err)
	}
	if _, ok := uf.Box["myimg"]; !ok {
		t.Error("shape-routed discovery did not register the box: doc as a box")
	}
}

// TestLoadUnified_DeploymentsSection — post-2026-05 kind-files cutover,
// the legacy v3 plural `deployments:` is hard-rejected at load time with
// a hint pointing at `charly migrate`. Pre-cutover this test
// asserted the alias was accepted; the inverse assertion enforces R5
// (no stale references).
func TestLoadUnified_DeploymentsSection(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "charly.yml", `version: 2026.203.2359
deployments:
  openclaw:
    port: ["8080:80"]
    target: container
`)
	_, _, err := LoadUnified(root)
	if err == nil {
		t.Fatal("expected hard-error for legacy v3 plural deployments:, got nil")
	}
	if !strings.Contains(err.Error(), "no kind discriminator") {
		t.Errorf("legacy `deployments:` must be hard-rejected by the node-form parser, got: %v", err)
	}
	if !strings.Contains(err.Error(), "deployments") {
		t.Errorf("error must mention the offending root-key, got: %v", err)
	}
}

func TestLoadUnified_ProjectConfig(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "charly.yml", `version: 2026.203.2359
defaults: { registry: r.example.com }
foo:
  candy:
    base: alpine
`)
	uf, _, err := LoadUnified(root)
	if err != nil {
		t.Fatalf("LoadUnified: %v", err)
	}
	cfg := uf.ProjectConfig()
	if cfg.Defaults.Registry != "r.example.com" {
		t.Errorf("Defaults.Registry = %q", cfg.Defaults.Registry)
	}
	if foo, _ := cfg.BoxConfig("foo"); foo.Base != "alpine" {
		t.Errorf("Box.foo.Base = %q", foo.Base)
	}
}
