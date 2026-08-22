package spec

// resolve_opts.go — the loader-config OPTIONS (ResolveOpts), the scan/load options threaded through
// the candy scan + project resolution. Relocated here from sdk/loaderkit (#55 loader cascade) so the
// ~14 charly-core call sites that only NAME this options struct reach it through the dedicated spec
// module and drop their loaderkit import. Its fields are the project build-vocabulary configs
// (*InitConfig / *DistroConfig / *BuilderConfig) — all native spec types since #72, carrying their
// own resolve methods (init_config_methods.go / distro_config_methods.go), so ResolveOpts references
// only sibling spec types and pulls in no mechanism package. DISTINCT from buildkit.ResolveOpts (the
// build-resolve options): this is the SCAN/LOAD options the candy scan + project validation consume;
// the buildkit resolvers never read ExtraCandyRefs/InitCfg/RequestedBoxes.

// ResolveOpts carries the scan/load options threaded through the candy scan + project resolution.
type ResolveOpts struct {
	IncludeDisabled      bool            // skip the `enabled: false` check
	IncludeDisabledNames map[string]bool // when non-empty, scope IncludeDisabled to these names only
	// RequestedBoxes are the explicit build targets (`charly box build <name>`). A qualified name
	// here (e.g. `charly.arch-builder`) is pulled into the resolved set even when it isn't reachable
	// as a base/builder of a root image — so a namespaced image can be an on-demand build target, not
	// only a transitive base. Bare names are ignored here (they resolve through the root loop).
	RequestedBoxes []string
	// ExtraCandyRefs are candy refs to collect IN ADDITION to the image/builder/kind:local-template
	// closure — specifically a DEPLOY's `add_candy:` candies. The image-closure walk (collectBox)
	// never reaches them, so a bed that add_candy's a host-side PLUGIN candy must pass its add_candy
	// refs here, or the plugin never enters the candy scan and loadProjectPlugins can't build/connect
	// it. NEVER read by the buildkit resolvers — consumed solely by the candy scan.
	ExtraCandyRefs []string
	// InitCfg is the project init: vocabulary (W9), threaded through so the candy scan can run the
	// cross-candy init-system host-completion pass (PopulateCandyInitSystem) BEFORE wrapping each
	// candy into the FINAL CandyReader. A caller that leaves this nil skips the pass (correct only for
	// a caller with no init-aware consumer downstream). NEVER read by the buildkit resolvers.
	InitCfg *InitConfig
	// DistroCfg / BuilderCfg are the project's build vocabulary (distro:/builder:), threaded through
	// so a resolve does not re-run the project load on every call (a caller with the triple, or a
	// multi-box loop, sets it once and skips the redundant reload; nil is byte-identical fallback).
	DistroCfg  *DistroConfig
	BuilderCfg *BuilderConfig
}

// BoxResolveOpts builds the ResolveOpts that scope a generate/build to a set of explicitly-named
// boxes. It is the SINGLE source of the box-selection rule (R3) for `charly box build` and
// `charly box generate` alike: an empty slice means "all enabled boxes" (no scoping); a non-empty
// slice pins those names into the resolved set (RequestedBoxes) and, when includeDisabled is set,
// relaxes the `enabled: false` gate for exactly those names (IncludeDisabledNames) so the override
// never widens the working set globally. Callers pass boxes already run through
// buildkit.NormalizeBoxArgs.
//
// It lived as charly's private `boxResolveOpts` until K-wave 2 cone R1 (A2). candy/plugin-build now
// builds the same value plugin-side to drive CollectRemoteRefsOpts itself, so the rule moves to the
// shared fabric module both sides import rather than being duplicated across the boundary — a pure
// ResolveOpts constructor over sibling spec types, pulling in no mechanism package.
func BoxResolveOpts(boxes []string, includeDisabled bool) ResolveOpts {
	opts := ResolveOpts{IncludeDisabled: includeDisabled}
	if len(boxes) == 0 {
		return opts
	}
	opts.RequestedBoxes = boxes
	if includeDisabled {
		opts.IncludeDisabledNames = make(map[string]bool, len(boxes))
		for _, name := range boxes {
			opts.IncludeDisabledNames[name] = true
		}
	}
	return opts
}

// WithLocalRawRefs returns opts with every local candy's RAW (pre-finalize) require:/candy: refs
// appended to ExtraCandyRefs. CollectRemoteRefsOpts's own "candy manifest require:/candy:" walk
// reads CandyView.Require/.IncludedCandy — the FINALIZED bare-string wire form (FinalizeCandyRefs
// strips a "@repo:vTAG" pin down to the bare graph-topology name; correct for its OWN consumers,
// ExpandCandy/ResolveCandyOrder, which are version-agnostic). Feeding that walk a wrapped view
// therefore leaves it structurally UNABLE to discover a local candy's pinned remote dep at all (a
// bare name never looks remote to IsRemoteCandyRef) — the confirmed root cause of a "depends:
// unknown candy" crash a live box/cachyos generate surfaced (a local candy's require: pins a
// remote plugin candy). So the raw pre-finalize refs (still carrying the full pin, from
// ScannedCandy.Refs) are harvested here and fed in as ExtraCandyRefs — the SAME mechanism a
// deploy's add_candy: already uses to reach a ref no base/builder/require edge would otherwise
// surface. A local (non-remote) ref is a harmless no-op (IsRemoteCandyRef gates it).
//
// Relocated from charly/layers.go in K-wave 2 cone R1 (A2) for the same reason as BoxResolveOpts:
// candy/plugin-build's own CollectRemoteRefsOpts call needs the identical augmentation, and a
// second copy across the module boundary is the R3 duplicate this program removes.
func WithLocalRawRefs(opts ResolveOpts, localScanned map[string]ScannedCandy) ResolveOpts {
	extraRefs := append([]string(nil), opts.ExtraCandyRefs...)
	for _, sc := range localScanned {
		for _, dep := range sc.Refs.Require {
			extraRefs = append(extraRefs, dep.Raw)
		}
		for _, dep := range sc.Refs.IncludedCandy {
			extraRefs = append(extraRefs, dep.Raw)
		}
	}
	opts.ExtraCandyRefs = extraRefs
	return opts
}

// ShouldIncludeDisabled reports whether name's disabled gate should be bypassed under opts.
// Centralizes the IncludeDisabled + IncludeDisabledNames interaction so call sites stay simple.
func (opts ResolveOpts) ShouldIncludeDisabled(name string) bool {
	if !opts.IncludeDisabled {
		return false
	}
	if len(opts.IncludeDisabledNames) == 0 {
		return true
	}
	return opts.IncludeDisabledNames[name]
}
