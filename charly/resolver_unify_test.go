package main

import (
	"testing"

	"github.com/opencharly/spec/spec"
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
func fixtureNamespacedProject(t *testing.T) (string, *spec.Config) {
	t.Helper()
	root := t.TempDir()
	writeFixture(t, root, "charly.yml", `version: "`+latestSchemaVersion.String()+`"
import:
  - sub: ./sub.yml
app:
  candy:
    base: quay.io/fedora/fedora:43
    build: [rpm]
    distro: [fedora]
    candy: []
`)
	writeFixture(t, root, "sub.yml", `version: "`+latestSchemaVersion.String()+`"
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

// TestResolveImage_QualifiedDelegates moved to candy/plugin-build/box_resolve_test.go (#55
// decoupling cone, Batch B, per orchestrator ruling): it asserts ResolveBox OUTPUT end to end
// (namespace-delegation resolution + name/base fields) — resolver-capability coverage.

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

// TestResolveAllImage_RequestedQualifiedTarget moved to candy/plugin-build/box_resolve_test.go
// (#55 decoupling cone, Batch B, per orchestrator ruling): it asserts ResolveAllBox OUTPUT end to
// end (explicit-request pull-in of a namespace-qualified target) — resolver-capability coverage.

// TestWalkBaseChain_RootInternalOnly guards the shared collector iterator's
// semantics: it follows ROOT-internal bases (so the 5 collectors keep walking
// the full same-repo chain) but STOPS at a namespace-qualified base. A
// namespaced base is a separately-built image that owns its own labels;
// descending into it would double-count candies the consumer also lists directly
// (the regression the id-uniqueness validator caught). Namespace descent is a
// name-resolution concern, not a per-image collection concern.
func TestWalkBaseChain_RootInternalOnly(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "charly.yml", `version: "`+latestSchemaVersion.String()+`"
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
	writeFixture(t, root, "sub.yml", `version: "`+latestSchemaVersion.String()+`"
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
