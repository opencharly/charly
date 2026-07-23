package main

import (
	"testing"

	"github.com/opencharly/sdk/spec"
)

// These tests pin the resolver-unification cutover: every command's
// box/local name resolution must descend import namespaces through the ONE
// namespace-aware mechanism (spec.SplitNamespaceRef / resolveBoxRef), instead
// of a flat root-only `c.Box[name]` lookup that silently misses (or
// truncates at) an imported namespace. Each test FAILS against the
// pre-cutover code.

// fixtureNamespacedProject writes a root project that imports a `sub`
// namespace, with `app` (root, external fedora base) and `sub.widget`
// (namespaced, external fedora base). `app` does NOT base off `sub.widget`, so
// `sub.widget` is NOT reachable as a base — exercising the explicit-target and
// direct-resolve paths rather than the base-reachability pull.
func fixtureNamespacedProject(t *testing.T) (string, *Config) {
	t.Helper()
	root := t.TempDir()
	writeFixture(t, root, "charly.yml", `version: 2026.203.2359
import:
  - sub: ./sub.yml
app:
  candy:
    base: quay.io/fedora/fedora:43
    build: [rpm]
    distro: [fedora]
    candy: []
`)
	writeFixture(t, root, "sub.yml", `version: 2026.203.2359
widget:
  candy:
    base: quay.io/fedora/fedora:43
    build: [rpm]
    distro: [fedora]
    candy: []
`)
	uf, _, err := LoadUnified(root)
	if err != nil {
		t.Fatalf("LoadUnified: %v", err)
	}
	return root, uf.ProjectConfig()
}

// TestResolveImage_QualifiedDelegates is the central-chokepoint guard:
// ResolveBox must resolve a namespace-qualified name by delegating into the
// owning namespace Config. Pre-fix, `c.Box["sub.widget"]` missed and this
// returned "image \"sub.widget\" not found".
func TestResolveImage_QualifiedDelegates(t *testing.T) {
	root, cfg := fixtureNamespacedProject(t)

	ri, err := ResolveBox(cfg, "sub.widget", "test", root, ResolveOpts{})
	if err != nil {
		t.Fatalf("ResolveBox(\"sub.widget\") must resolve via namespace delegation: %v", err)
	}
	if ri.Name != "widget" {
		t.Errorf("resolved name = %q, want %q (leaf, resolved in the namespace context)", ri.Name, "widget")
	}
	if ri.Base != "quay.io/fedora/fedora:43" {
		t.Errorf("resolved base = %q, want the namespace image's base", ri.Base)
	}

	// Bare names still resolve in root, unchanged.
	if _, err := ResolveBox(cfg, "app", "test", root, ResolveOpts{}); err != nil {
		t.Errorf("bare ResolveBox(\"app\") regressed: %v", err)
	}
	// A genuinely-missing namespace still errors clearly.
	if _, err := ResolveBox(cfg, "nope.widget", "test", root, ResolveOpts{}); err == nil {
		t.Error("ResolveBox(\"nope.widget\") should error: no such namespace")
	}
}

// TestFindImageByLeaf covers the discovery dual used by ensure-image's
// build-fallback: a bare basename (extracted from a full registry ref) must be
// found wherever it lives — root or an imported namespace — and returned as the
// qualified ref the build/resolve paths accept.
func TestFindImageByLeaf(t *testing.T) {
	_, cfg := fixtureNamespacedProject(t)

	if got, ok := cfg.FindBoxByLeaf("app"); !ok || got != "app" {
		t.Errorf("findBoxByLeaf(\"app\") = %q,%v; want \"app\",true (root hit, bare)", got, ok)
	}
	if got, ok := cfg.FindBoxByLeaf("widget"); !ok || got != "sub.widget" {
		t.Errorf("findBoxByLeaf(\"widget\") = %q,%v; want \"sub.widget\",true (namespaced hit, qualified)", got, ok)
	}
	if got, ok := cfg.FindBoxByLeaf("absent"); ok {
		t.Errorf("findBoxByLeaf(\"absent\") = %q,true; want \"\",false", got)
	}
}

// TestResolveAllImage_RequestedQualifiedTarget guards the build-target path:
// an explicitly-requested qualified box that is NOT a base/builder of any
// root box must still land in the resolved set (so filterBox / the build
// graph accept `charly box build sub.widget` and the ensure-image build-fallback
// for a namespaced builder). Pre-fix it was absent.
func TestResolveAllImage_RequestedQualifiedTarget(t *testing.T) {
	root, cfg := fixtureNamespacedProject(t)

	// Without RequestedBoxes, sub.widget is not reachable, so not pulled.
	base, err := ResolveAllBox(cfg, "test", root, ResolveOpts{})
	if err != nil {
		t.Fatalf("ResolveAllBox: %v", err)
	}
	if _, present := base["sub.widget"]; present {
		t.Fatal("sub.widget should NOT be in the resolved set without an explicit request (it is not a base of any root image)")
	}

	// With it requested, it is pulled under its fully-qualified key.
	withReq, err := ResolveAllBox(cfg, "test", root, ResolveOpts{RequestedBoxes: []string{"sub.widget"}})
	if err != nil {
		t.Fatalf("ResolveAllBox(RequestedImages): %v", err)
	}
	if _, present := withReq["sub.widget"]; !present {
		t.Errorf("requested qualified target sub.widget absent from resolved set (keys: %v)", keysOf(withReq))
	}
}

// TestWalkBaseChain_RootInternalOnly guards the shared collector iterator's
// semantics: it follows ROOT-internal bases (so the 5 collectors keep walking
// the full same-repo chain) but STOPS at a namespace-qualified base. A
// namespaced base is a separately-built image that owns its own labels;
// descending into it would double-count candies the consumer also lists directly
// (the regression the id-uniqueness validator caught). Namespace descent is a
// name-resolution concern, not a per-image collection concern.
func TestWalkBaseChain_RootInternalOnly(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "charly.yml", `version: 2026.203.2359
import:
  - sub: ./sub.yml
parent:
  candy:
    base: quay.io/fedora/fedora:43
    build: [rpm]
    distro: [fedora]
    candy: []
child:
  candy:
    base: parent
    build: [rpm]
    distro: [fedora]
    candy: []
nschild:
  candy:
    base: sub.widget
    build: [rpm]
    distro: [fedora]
    candy: []
`)
	writeFixture(t, root, "sub.yml", `version: 2026.203.2359
widget:
  candy:
    base: quay.io/fedora/fedora:43
    build: [rpm]
    distro: [fedora]
    candy: []
`)
	uf, _, err := LoadUnified(root)
	if err != nil {
		t.Fatalf("LoadUnified: %v", err)
	}
	cfg := uf.ProjectConfig()

	// Root-internal base IS followed.
	if got := chainNames(cfg.WalkBaseChain("child")); len(got) != 2 || got[0] != "child" || got[1] != "parent" {
		t.Errorf("walkBaseChain(\"child\") = %v; want [child, parent] (root-internal base followed)", got)
	}
	// Namespace-qualified base is NOT descended — the walk stops at the boundary
	// so per-image collection doesn't double-count the separately-built base.
	if got := chainNames(cfg.WalkBaseChain("nschild")); len(got) != 1 || got[0] != "nschild" {
		t.Errorf("walkBaseChain(\"nschild\") = %v; want [nschild] (stops at namespaced base)", got)
	}
}

func chainNames(nodes []spec.BaseChainNode) []string {
	out := make([]string, len(nodes))
	for i, n := range nodes {
		out[i] = n.Name
	}
	return out
}
