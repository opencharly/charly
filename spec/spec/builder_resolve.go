package spec

import (
	"maps"
	"slices"
)

// builder_resolve.go — the builder-map VALUE-PRIMITIVE cluster (#55 coneSpecBuilder, relocated
// from sdk/buildkit/config_resolve.go + sdk/buildkit/distro_builder_map.go). Every function here
// is PURE VALUE computation over already-loaded spec types (*Config, BoxConfig, BuilderMap,
// DistroBuilderCandidate) — no LoadUnified, no host I/O, no *ResolvedBox, no buildkit/deploykit
// import. This is the CONTRACT-layer half of builder resolution: the "resolve" naming is the SAME
// category as the existing *Config config-nav methods (ResolveBoxRef/WalkBaseChainDistro/
// AllBoxNames). The BUILD behavior (ResolveBox/ResolveAllBox, which return *buildkit.ResolvedBox)
// STAYS in buildkit — those call spec.ResolveEffectiveBuilder for the builder-map leg.
//
// Why these moved out of buildkit: they landed in buildkit by PROXIMITY to ResolveBox during
// FLOOR-SLIM Unit 5, NOT by their own nature — the buildkit config_resolve.go header's
// *ResolvedBox rejection applies ONLY to ResolveBox/ResolveAllBox (whose signatures unavoidably
// touch buildkit's own *ResolvedBox). These 5 symbols (4 functions + the DistroBuilderCandidate
// type, now CUE-sourced in schema/resolvedbox.cue alongside #BuilderMap) operate ONLY on spec
// types + stdlib (maps/slices), so they are CONTRACT → spec/spec (plan Rule 2 + step 3: the
// resolve-cluster VALUE TYPES move to spec first; the BUILD behavior stays in buildkit/plugin-build).
//
// Free functions taking *Config as first param (Go forbids a package outside a type's own package
// from adding methods to it; the existing *Config methods are methods only because they are
// defined here where the type lives). Mirrors the form they had as buildkit free functions.

// PickDistroBuilder returns the builder map of the first candidate (in the
// given, caller-sorted order — determinism is the caller's responsibility, so
// the result is stable when more than one candidate shares a distro tag)
// whose Distro contains a tag, walking distroTags in priority order (most
// specific first, e.g. ["cachyos","arch"] or ["fedora:43","fedora"]). Only
// candidates with a non-empty Builder are considered.
func PickDistroBuilder(candidates []DistroBuilderCandidate, distroTags []string) BuilderMap {
	if len(distroTags) == 0 {
		return nil
	}
	for _, tag := range distroTags {
		for _, c := range candidates {
			if len(c.Builder) == 0 {
				continue
			}
			if slices.Contains(c.Distro, tag) {
				return c.Builder
			}
		}
	}
	return nil
}

// distroBuilderMap returns the builder map of the root-namespace image that owns the given
// distro — the distro-keyed builder default. distroTags is the image's resolved distro in
// priority order; the first tag with a matching root image wins.
func distroBuilderMap(cfg *Config, distroTags []string) BuilderMap {
	names := cfg.AllBoxNames()
	candidates := make([]DistroBuilderCandidate, 0, len(names))
	for _, name := range names {
		img, _ := cfg.BoxConfig(name)
		candidates = append(candidates, DistroBuilderCandidate{Name: name, Distro: img.Distro, Builder: img.Builder})
	}
	return PickDistroBuilder(candidates, distroTags)
}

// ResolveEffectiveBuilder computes an image's effective builder map via the SINGLE canonical
// precedence, lowest→highest: defaults.builder → distro-keyed default → direct local base →
// per-image override — then self-references are filtered. EVERY builder-consuming path calls
// this so resolution can never drift between commands.
func ResolveEffectiveBuilder(cfg *Config, name string, distro []string, base string, isExternalBase bool, imgBuilder BuilderMap) BuilderMap {
	out := make(BuilderMap)
	maps.Copy(out, cfg.Defaults.Builder)
	maps.Copy(out, distroBuilderMap(cfg, distro))
	if !isExternalBase {
		// DELIBERATELY flat (not ResolveBoxRef): a base's builder map is only inherited when the
		// base is ROOT-local. A namespace-qualified base intentionally does NOT contribute its
		// builder map here.
		if baseImg, ok := cfg.BoxConfig(base); ok {
			maps.Copy(out, baseImg.Builder)
		}
	}
	maps.Copy(out, imgBuilder)
	for typ, b := range out {
		if b == name {
			delete(out, typ)
		}
	}
	return out
}

// EffectiveBuilderForBox computes the builder image refs an image will build against, from a RAW
// BoxConfig — the FETCH-path counterpart to ResolveBox's resolved-value path. Both end at the ONE
// canonical ResolveEffectiveBuilder.
func EffectiveBuilderForBox(cfg *Config, name string, img BoxConfig) BuilderMap {
	base := "scratch"
	isExternalBase := true
	if img.From == "" && !img.DataImage {
		base = img.Base
		if base == "" {
			base = cfg.Defaults.Base
		}
		if base == "" {
			base = "quay.io/fedora/fedora:43"
		}
		if baseImg, _, isInternal := cfg.ResolveBoxRef(base); isInternal && baseImg.IsEnabled() {
			isExternalBase = false
		}
	}
	distro := img.Distro
	if len(distro) == 0 {
		distro = cfg.WalkBaseChainDistro(base)
	}
	if len(distro) == 0 {
		distro = cfg.Defaults.Distro
	}
	return ResolveEffectiveBuilder(cfg, name, distro, base, isExternalBase, img.Builder)
}
