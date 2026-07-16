package main

import (
	"testing"

	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/spec"
)

// TestComputeEffectiveVersions covers the image-version derivation that feeds
// the content-stable ai.opencharly.version label: a dedicated version: wins;
// otherwise the highest candy version across the chain; a candyless image
// recurses to its internal base; a candyless external-base image with no version
// is a HARD ERROR (no build-timestamp fallback — the per-kind versioning cutover
// dropped all backward compat).
func TestComputeEffectiveVersions(t *testing.T) {
	layers := map[string]*Candy{
		"a": {Name: "a", Version: "2026.100.0000"},
		"b": {Name: "b", Version: "2026.200.0000"}, // newest candy
	}
	images := map[string]*buildkit.ResolvedBox{
		// dedicated version wins over the (newer) candy versions.
		"dedicated": {Name: "dedicated", Version: "2026.050.0000", Candy: []string{"a", "b"}, IsExternalBase: true, Base: "quay.io/x:1"},
		// no dedicated version -> highest candy version (b = 2026.200.0000).
		"derived": {Name: "derived", Candy: []string{"a", "b"}, IsExternalBase: true, Base: "quay.io/x:1"},
		// bare base: candyless + external + dedicated version (what `charly migrate` backfills).
		"barebase": {Name: "barebase", Version: "2026.300.0000", IsExternalBase: true, Base: "quay.io/x:1"},
		// candyless on an INTERNAL base -> recurse to the base's effective version.
		"passthrough": {Name: "passthrough", Base: "barebase"},
	}
	g := &Generator{Boxes: images, Candies: layers}
	if err := g.computeEffectiveVersions(); err != nil {
		t.Fatalf("computeEffectiveVersions: %v", err)
	}

	cases := map[string]string{
		"dedicated":   "2026.050.0000", // dedicated wins
		"derived":     "2026.200.0000", // highest candy version
		"barebase":    "2026.300.0000", // dedicated bare-base version
		"passthrough": "2026.300.0000", // recursed to barebase
	}
	for name, want := range cases {
		if got := images[name].EffectiveVersion; got != want {
			t.Errorf("%s: EffectiveVersion = %q, want %q", name, got, want)
		}
	}

	// A candy bump propagates to a deriving image's identity.
	layers["b"].Version = "2026.400.0000"
	g2 := &Generator{Boxes: map[string]*buildkit.ResolvedBox{
		"derived": {Name: "derived", Candy: []string{"a", "b"}, IsExternalBase: true, Base: "quay.io/x:1"},
	}, Candies: layers}
	if err := g2.computeEffectiveVersions(); err != nil {
		t.Fatal(err)
	}
	if got := g2.Boxes["derived"].EffectiveVersion; got != "2026.400.0000" {
		t.Errorf("after candy bump: EffectiveVersion = %q, want 2026.400.0000", got)
	}

	// Hard error: candyless external-base image with no version (no fallback).
	gErr := &Generator{
		Boxes:   map[string]*buildkit.ResolvedBox{"orphan": {Name: "orphan", IsExternalBase: true, Base: "quay.io/x:1"}},
		Candies: map[string]*Candy{},
	}
	if err := gErr.computeEffectiveVersions(); err == nil {
		t.Error("expected a hard error for a candyless external-base image with no version:")
	}
}

// TestBakePluginImpliesRequire_FeedsEffectiveVersion proves the S0 hash-gap fix:
// a candy that declares ONLY `bake_plugin: <ref>` (no explicit require:) still
// pulls the baked plugin candy into its require chain, so the baked plugin's
// version: contributes to the composing image's EffectiveVersion — and bumping
// the baked plugin's version bumps the image identity. Without the implication
// in populateCandyFromYAML, ResolveCandyOrder never walks to the plugin candy,
// the candy set excludes it, and EffectiveVersion stays pinned to the (lower)
// consumer version — a stale baked plugin would escape rebuild. This test FAILS
// without the bake_plugin→require implication.
func TestBakePluginImpliesRequire_FeedsEffectiveVersion(t *testing.T) {
	// The consumer candy declares ONLY bake_plugin (no explicit require:).
	consumer := &Candy{Name: "consumer-candy"}
	populateCandyFromYAML(consumer, &spec.CandyYAML{
		Version:    "2026.100.0000", // lower than the baked plugin below
		BakePlugin: []string{"plugin-baked"},
	})

	// The implication: the baked plugin ref is now in the require set.
	if !candyRequires(consumer, "plugin-baked") {
		t.Fatalf("bake_plugin did not imply require: consumer.Require = %v", consumer.Require)
	}
	// And it was not double-added (it's a set).
	if n := countCandyRequire(consumer, "plugin-baked"); n != 1 {
		t.Fatalf("plugin-baked appears %d times in require, want exactly 1", n)
	}

	plugin := &Candy{Name: "plugin-baked", Version: "2026.200.0000"} // the newest version
	layers := map[string]*Candy{
		"consumer-candy": consumer,
		"plugin-baked":   plugin,
	}
	images := map[string]*buildkit.ResolvedBox{
		// An image composing ONLY the consumer candy. Its EffectiveVersion must
		// reflect the baked plugin's (higher) version, reached via the implied require.
		"img": {Name: "img", Candy: []string{"consumer-candy"}, IsExternalBase: true, Base: "quay.io/x:1"},
	}
	g := &Generator{Boxes: images, Candies: layers}
	if err := g.computeEffectiveVersions(); err != nil {
		t.Fatalf("computeEffectiveVersions: %v", err)
	}
	if got := images["img"].EffectiveVersion; got != "2026.200.0000" {
		t.Fatalf("EffectiveVersion = %q, want 2026.200.0000 (the baked plugin's version reached via the implied require)", got)
	}

	// Bumping the baked plugin's version bumps the composing image's identity.
	plugin.Version = "2026.300.0000"
	g2 := &Generator{Boxes: map[string]*buildkit.ResolvedBox{
		"img": {Name: "img", Candy: []string{"consumer-candy"}, IsExternalBase: true, Base: "quay.io/x:1"},
	}, Candies: layers}
	if err := g2.computeEffectiveVersions(); err != nil {
		t.Fatal(err)
	}
	if got := g2.Boxes["img"].EffectiveVersion; got != "2026.300.0000" {
		t.Fatalf("after baked-plugin bump: EffectiveVersion = %q, want 2026.300.0000", got)
	}

	// Declaring BOTH bake_plugin and an explicit require of the same ref does not
	// double-add (the redundant case the cutover removes from candy/charly-mcp).
	both := &Candy{Name: "both"}
	populateCandyFromYAML(both, &spec.CandyYAML{
		Version:    "2026.100.0000",
		Require:    []string{"plugin-baked"},
		BakePlugin: []string{"plugin-baked"},
	})
	if n := countCandyRequire(both, "plugin-baked"); n != 1 {
		t.Fatalf("explicit require + bake_plugin double-added: plugin-baked appears %d times, want 1", n)
	}
}

func candyRequires(l *Candy, bare string) bool { return countCandyRequire(l, bare) > 0 }

func countCandyRequire(l *Candy, bare string) int {
	n := 0
	for _, r := range l.Require {
		if r.Bare() == bare {
			n++
		}
	}
	return n
}
