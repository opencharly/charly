package main

import (
	"testing"
)

// TestImportList_Unmarshal covers the mixed-shape import list: bare strings
// (flat root imports) and single-key maps (namespaced child imports).
func TestImportList_Unmarshal(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "charly.yml", `version: 2026.225.1508
import:
  - build.yml
  - sub: ./sub.yml
`)
	writeFixture(t, root, "build.yml", `defaults:
  build: [rpm]
`)
	writeFixture(t, root, "sub.yml", `version: 2026.225.1508
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
// qualified image ref through the projected Config — the LOADER's own product
// (cfg.ResolveBoxRef), not what ResolveBox makes of it. The companion
// IsExternalBase-classification assertion (does ResolveBox correctly classify
// a namespace-resolved base as internal) moved to
// candy/plugin-build/box_resolve_test.go as
// TestResolveBox_NamespacedBaseIsInternal (#55 decoupling cone, Batch B, per
// orchestrator ruling: split by assertion — what the loader produced is
// charly's to test, what ResolveBox makes of it is plugin-build's).
func TestResolveImageRef_Qualified(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "charly.yml", `version: 2026.225.1508
import:
  - sub: ./sub.yml
app:
  candy:
    base: sub.widget
    build: [rpm]
    distro: [fedora]
    candy: []
`)
	writeFixture(t, root, "sub.yml", `version: 2026.225.1508
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
}

// TestImportNamespace_MutualCycle verifies the main<->sub mutual import is
// cycle-broken at load (the shared resolved-ref cache).
func TestImportNamespace_MutualCycle(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "charly.yml", `version: 2026.225.1508
import:
  - sub: ./sub
app:
  candy:
    base: sub.widget
    build: [rpm]
    distro: [fedora]
`)
	writeFixture(t, root, "sub/charly.yml", `version: 2026.225.1508
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

// TestResolveNamespacedBase_BuilderRefRequalified / TestResolveBuilder_DistroKeyed_NoExplicitMap
// (+ the keysOf helper) moved to candy/plugin-build/box_resolve_test.go (#55 decoupling cone,
// Batch B, per orchestrator ruling — split by assertion): both assert resolveAllBoxTest/ResolveAllBox
// OUTPUT (builder-ref requalification, distro-keyed builder defaults) end to end, zero loader-product
// assertion — resolver-capability coverage, not charly-loader coverage. Rebuilt there against a
// literal *spec.UnifiedFile fixture (a self-referential Namespaces pointer tree + .ProjectConfig())
// instead of LoadUnified + real files.
