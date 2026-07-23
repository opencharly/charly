package main

import (
	"testing"

	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/kit"
)

// TestImportList_Unmarshal covers the mixed-shape import list: bare strings
// (flat root imports) and single-key maps (namespaced child imports).
func TestImportList_Unmarshal(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "charly.yml", `version: 2026.204.1223
import:
  - build.yml
  - sub: ./sub.yml
`)
	writeFixture(t, root, "build.yml", `defaults:
  build: [rpm]
`)
	writeFixture(t, root, "sub.yml", `version: 2026.204.1223
widget:
  candy:
    base: quay.io/fedora/fedora:43
    distro: [fedora]
`)
	uf, _, err := LoadUnified(root)
	if err != nil {
		t.Fatalf("LoadUnified: %v", err)
	}
	// Flat import merged build.yml into root.
	if len(uf.Defaults.Build) != 1 || uf.Defaults.Build[0] != "rpm" {
		t.Errorf("flat import not merged: Defaults.Build = %v", uf.Defaults.Build)
	}
	// Namespaced import mounted under "sub", NOT flat-merged into root.
	if _, leaked := uf.Box["widget"]; leaked {
		t.Error("namespaced entry leaked into root Image map")
	}
	if uf.Namespaces["sub"] == nil {
		t.Fatal("namespace 'sub' not mounted")
	}
	if _, ok := uf.Namespaces["sub"].Box["widget"]; !ok {
		t.Error("sub.widget missing from the 'sub' namespace")
	}
}

// TestResolveImageRef_Qualified checks namespace-relative resolution of a
// qualified image ref through the projected Config.
func TestResolveImageRef_Qualified(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "charly.yml", `version: 2026.204.1223
import:
  - sub: ./sub.yml
app:
  candy:
    base: sub.widget
    build: [rpm]
    distro: [fedora]
    candy: []
`)
	writeFixture(t, root, "sub.yml", `version: 2026.204.1223
widget:
  candy:
    base: quay.io/fedora/fedora:43
    build: [rpm]
    distro: [fedora]
`)
	uf, _, err := LoadUnified(root)
	if err != nil {
		t.Fatalf("LoadUnified: %v", err)
	}
	cfg := uf.ProjectConfig()
	// Bare local name resolves in root.
	if _, _, ok := cfg.ResolveBoxRef("app"); !ok {
		t.Error("bare ref 'app' did not resolve in root")
	}
	// Qualified ref descends into the namespace.
	wImg, wCfg, ok := cfg.ResolveBoxRef("sub.widget")
	if !ok {
		t.Fatal("qualified ref 'sub.widget' did not resolve")
	}
	if wImg.Base != "quay.io/fedora/fedora:43" {
		t.Errorf("sub.widget base = %q", wImg.Base)
	}
	if wCfg == cfg {
		t.Error("qualified ref should resolve in the sub-namespace Config, not root")
	}
	// app's base (sub.widget) must be classified INTERNAL (resolves via namespace),
	// not mistaken for an external OCI URL.
	ri, err := ResolveBox(cfg, "app", "test", root, ResolveOpts{})
	if err != nil {
		t.Fatalf("ResolveBox(app): %v", err)
	}
	if ri.IsExternalBase {
		t.Error("app.base = sub.widget should be IsExternalBase=false (resolved through namespace)")
	}
}

// TestImportNamespace_MutualCycle verifies the main<->sub mutual import is
// cycle-broken at load (the shared resolved-ref cache).
func TestImportNamespace_MutualCycle(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "charly.yml", `version: 2026.204.1223
import:
  - sub: ./sub
app:
  candy:
    base: sub.widget
    build: [rpm]
    distro: [fedora]
`)
	writeFixture(t, root, "sub/charly.yml", `version: 2026.204.1223
import:
  - up: ../
widget:
  candy:
    base: quay.io/fedora/fedora:43
    build: [rpm]
    distro: [fedora]
`)
	uf, _, err := LoadUnified(root)
	if err != nil {
		t.Fatalf("LoadUnified (mutual import must not loop): %v", err)
	}
	if uf.Namespaces["sub"] == nil || uf.Namespaces["sub"].Namespaces["up"] == nil {
		t.Fatal("mutual import namespaces not mounted")
	}
}

// TestResolveNamespacedBase_BuilderRefRequalified is the regression guard for the
// cross-namespace builder-ref leak. When the root consumes a namespaced base
// (`app: base: sub.widget`) whose builder map references the base's OWN namespace
// (`widget: builder: {pixi: up.archlike-builder}`, where sub imports root as `up`),
// pullNamespacedBox must re-qualify that builder ref (`up.archlike-builder` ->
// `sub.up.archlike-builder`) — exactly as it re-qualifies `base:` — so it resolves
// from the root config and matches the key the builder image is pulled under.
//
// Before the fix this failed with
//
//	import namespace "up" not found (resolving "up.archlike-builder")
//
// because the builder ref was re-resolved from root (no `up` namespace there).
// Mirrors the real selkies-labwc (`builder: charly.arch-builder`) consumed by main's
// android-emulator (`base: cachyos.selkies-labwc`). The shape — a namespaced base
// with BOTH buildable candies AND a namespace-relative builder map — is the exact
// combination the prior tests never exercised.
func TestResolveNamespacedBase_BuilderRefRequalified(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "charly.yml", `version: 2026.204.1223
import:
  - sub: ./sub
app:
  candy:
    base: sub.widget
    build: [rpm]
    distro: [fedora]
archlike-builder:
  candy:
    base: quay.io/fedora/fedora:43
    build: [rpm]
    produce: [pixi]
    distro: [fedora]
`)
	writeFixture(t, root, "sub/charly.yml", `version: 2026.204.1223
import:
  - up: ../
buildable:
  candy:
    plan:
      - run: install
        command: "true"
widget:
  candy:
    base: quay.io/fedora/fedora:43
    build: [pac, aur]
    builder:
      pixi: up.archlike-builder
    distro: [fedora]
    candy: [buildable]
`)
	uf, _, err := LoadUnified(root)
	if err != nil {
		t.Fatalf("LoadUnified: %v", err)
	}
	cfg := uf.ProjectConfig()
	resolved, err := ResolveAllBox(cfg, "test", root, ResolveOpts{})
	if err != nil {
		t.Fatalf("ResolveAllBox must NOT fail when a namespaced base's builder ref points into the base's own namespace: %v", err)
	}
	w, ok := resolved["sub.widget"]
	if !ok {
		t.Fatal("sub.widget not pulled into the resolved set")
	}
	if got := w.Builder.BuilderFor("pixi"); got != "sub.up.archlike-builder" {
		t.Errorf("widget builder ref not re-qualified: got %q, want %q", got, "sub.up.archlike-builder")
	}
	if _, ok := resolved["sub.up.archlike-builder"]; !ok {
		t.Errorf("re-qualified builder image sub.up.archlike-builder absent from resolved set (keys: %v)", keysOf(resolved))
	}
}

// TestResolveBuilder_DistroKeyed_NoExplicitMap is the regression guard for the
// distro-keyed builder default: an image whose base is reached through an import
// namespace and resolves to a cachyos/Arch distro must auto-select arch-builder
// WITHOUT any per-image `builder:` declaration — the root `arch` image (whose
// distro: matches and whose bare arch-builder ref resolves in root) supplies it.
// Without the fix this resolves fedora-builder (the Fedora-only defaults.builder)
// — the exact bug that silently built a Fedora builder for cachyos images.
func TestResolveBuilder_DistroKeyed_NoExplicitMap(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "charly.yml", `version: 2026.204.1223
import:
  - sub: ./sub
defaults:
  builder:
    pixi: fedora-builder
    npm: fedora-builder
arch:
  candy:
    base: quay.io/cachyos/cachyos:latest
    build: [pac]
    builder:
      pixi: arch-builder
      npm: arch-builder
    distro: [arch]
arch-builder:
  candy:
    base: quay.io/cachyos/cachyos:latest
    build: [pac]
    produce: [pixi, npm]
    distro: [arch]
fedora-builder:
  candy:
    base: quay.io/fedora/fedora:43
    build: [rpm]
    produce: [pixi, npm]
    distro: [fedora]
cachyos-app:
  candy:
    base: sub.cachyos
fedora-app:
  candy:
    base: sub.fedora
`)
	writeFixture(t, root, "sub/charly.yml", `version: 2026.204.1223
import:
  - up: ../
cachyos:
  candy:
    base: quay.io/cachyos/cachyos:latest
    build: [pac, aur]
    distro: [cachyos, arch]
fedora:
  candy:
    base: quay.io/fedora/fedora:43
    build: [rpm]
    distro: [fedora]
`)
	uf, _, err := LoadUnified(root)
	if err != nil {
		t.Fatalf("LoadUnified: %v", err)
	}
	cfg := uf.ProjectConfig()
	resolved, err := ResolveAllBox(cfg, "test", root, ResolveOpts{})
	if err != nil {
		t.Fatalf("ResolveAllBox: %v", err)
	}
	app, ok := resolved["cachyos-app"]
	if !ok {
		t.Fatalf("cachyos-app not resolved (keys: %v)", keysOf(resolved))
	}
	// THE FIX: namespaced cachyos/arch base → arch-builder, no per-image map.
	if got := app.Builder.BuilderFor("pixi"); got != "arch-builder" {
		t.Errorf("cachyos-app pixi builder = %q, want arch-builder (distro-keyed default)", got)
	}
	if got := app.Builder.BuilderFor("npm"); got != "arch-builder" {
		t.Errorf("cachyos-app npm builder = %q, want arch-builder", got)
	}
	// Guard: a fedora-distro image must still resolve fedora-builder.
	fa, ok := resolved["fedora-app"]
	if !ok {
		t.Fatalf("fedora-app not resolved")
	}
	if got := fa.Builder.BuilderFor("pixi"); got != "fedora-builder" {
		t.Errorf("fedora-app pixi builder = %q, want fedora-builder (no regression)", got)
	}
}

func keysOf(m map[string]*buildkit.ResolvedBox) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	kit.SortStrings(ks)
	return ks
}
