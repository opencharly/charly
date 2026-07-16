package main

import (
	"fmt"
	"github.com/opencharly/sdk/spec"
	"sort"
	"strings"
	"testing"
)

// canonicalIntermediates serializes a resolved box map into a stable,
// order-independent string capturing every field that determines an
// intermediate's identity (and thus its Containerfile + FROM SHA). Two runs of
// ComputeIntermediates that produce the same canonical string are guaranteed to
// emit identical build artifacts.
func canonicalIntermediates(m map[string]*ResolvedBox) string {
	lines := make([]string, 0, len(m))
	for name, img := range m {
		lines = append(lines, fmt.Sprintf("%s|base=%s|candy=%v|builds=%v|distro=%v|auto=%v",
			name, img.Base, img.Candy, img.BuildFormats, img.Distro, img.Auto))
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// determinismFixture builds a scenario that REPRODUCES the map-iteration
// non-determinism the fix closes (verified: with the fix reverted, the
// determinism tests below fail). Two consuming images share a candy prefix off
// the SAME external base, branch into distinct leaves, and carry DIFFERENT build
// formats / distro tags — so the auto-intermediate created at the branch point
// unions their formats, and the UNION ORDER depends on the order
// collectSubtreeBoxes walks the trie children (Fix C). A second pair of consumers
// adds conflicting authored candy-list orders (A-before-B vs B-before-A) so the
// cycle-skipping edge insertion in GlobalCandyOrder is iteration-order sensitive
// (Fix A), and the two external bases share a short name ("img") so their
// auto-intermediates collide and the `-2` suffix assignment depends on
// sibling-group processing order (Fix B).
func determinismFixture() (map[string]*ResolvedBox, map[string]*Candy, *Config) {
	layers := map[string]*Candy{
		"core":   {Name: "core", plan: []Step{{Run: "build", Op: cmdOp("true")}}},
		"shared": {Name: "shared", Require: toCandyRefs([]string{"core"}), HasPixiToml: true},
		"leafA":  {Name: "leafA", Require: toCandyRefs([]string{"shared"}), HasPixiToml: true},
		"leafB":  {Name: "leafB", Require: toCandyRefs([]string{"shared"}), HasPackageJson: true},
		// Conflicting-authored-order pair: list [x, y] vs [y, x].
		"x": {Name: "x", plan: []Step{{Run: "build", Op: cmdOp("true")}}},
		"y": {Name: "y", plan: []Step{{Run: "build", Op: cmdOp("true")}}},
	}

	mk := func(name, base string, candy []string, builds BuildFormats, distro []string) *ResolvedBox {
		return &ResolvedBox{
			Name: name, Base: base, IsExternalBase: true,
			Candy: candy, Tag: "v1", Registry: "r",
			FullTag: "r/" + name + ":v1", Pkg: "rpm",
			BuildFormats: builds, Distro: distro,
		}
	}
	// Two external bases that SHORTEN to the same name ("img") so their
	// branch-point auto-intermediates ("img-shared") collide → -2 suffix order
	// is sibling-group-order sensitive.
	images := map[string]*ResolvedBox{
		// base A group: shared → {leafA, leafB}, consumers carry different formats
		"a-one": mk("a-one", "reg-a/img:1", []string{"leafA", "x", "y"}, BuildFormats{"pac", "aur"}, []string{"arch"}),
		"a-two": mk("a-two", "reg-a/img:1", []string{"leafB", "y", "x"}, BuildFormats{"pac", "x264"}, []string{"arch", "arch-extra"}),
		// base B group: same short name "img", same branch shape
		"b-one": mk("b-one", "reg-b/img:1", []string{"leafA"}, BuildFormats{"pac", "aur"}, []string{"arch"}),
		"b-two": mk("b-two", "reg-b/img:1", []string{"leafB"}, BuildFormats{"pac", "x264"}, []string{"arch"}),
	}

	cfg := &Config{
		Defaults: spec.BoxConfig{Registry: "r", Build: BuildFormats{"pac"}, Distro: []string{"arch"}},
		Box: boxMapOf(map[string]spec.BoxConfig{
			"a-one": {Candy: []string{"leafA", "x", "y"}},
			"a-two": {Candy: []string{"leafB", "y", "x"}},
			"b-one": {Candy: []string{"leafA"}},
			"b-two": {Candy: []string{"leafB"}},
		}),
	}
	return images, layers, cfg
}

// TestComputeIntermediates_Deterministic runs the intermediate computation many
// times on the same input and asserts byte-identical results every time. Go
// randomizes map iteration, so before the determinism fix (sorted iteration in
// GlobalCandyOrder + sibling-group processing + subtree collection) this fails
// with overwhelming probability across this many trials; after, it is stable.
func TestComputeIntermediates_Deterministic(t *testing.T) {
	const trials = 50
	var want string
	for i := range trials {
		images, layers, cfg := determinismFixture()
		result, err := ComputeIntermediates(images, layers, cfg, "v1")
		if err != nil {
			t.Fatalf("trial %d: ComputeIntermediates() error = %v", i, err)
		}
		got := canonicalIntermediates(result)
		if i == 0 {
			want = got
			continue
		}
		if got != want {
			t.Fatalf("trial %d produced a different result than trial 0:\n--- trial 0 ---\n%s\n--- trial %d ---\n%s", i, want, i, got)
		}
	}
}

// TestGlobalCandyOrder_Deterministic asserts the global candy order itself is
// stable across many runs — the upstream invariant the intermediate
// determinism depends on.
func TestGlobalCandyOrder_Deterministic(t *testing.T) {
	const trials = 50
	var want string
	for i := range trials {
		images, layers, _ := determinismFixture()
		order, err := GlobalCandyOrder(images, layers)
		if err != nil {
			t.Fatalf("trial %d: GlobalCandyOrder() error = %v", i, err)
		}
		got := strings.Join(order, ",")
		if i == 0 {
			want = got
			continue
		}
		if got != want {
			t.Fatalf("trial %d global order differs:\n want %s\n got  %s", i, want, got)
		}
	}
}

// TestSortedSiblingKeys asserts the helper returns keys ordered by (base, uid).
func TestSortedSiblingKeys(t *testing.T) {
	m := map[siblingKey][]string{
		{Base: "fedora", UID: 1000}: {"x"},
		{Base: "arch", UID: 0}:      {"y"},
		{Base: "fedora", UID: 0}:    {"z"},
		{Base: "arch", UID: 1000}:   {"w"},
	}
	got := sortedSiblingKeys(m)
	want := []siblingKey{
		{Base: "arch", UID: 0},
		{Base: "arch", UID: 1000},
		{Base: "fedora", UID: 0},
		{Base: "fedora", UID: 1000},
	}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("key[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}
