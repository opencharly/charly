package main

import (
	"errors"
	"fmt"

	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/spec"
)

// config.go — FLOOR-SLIM Unit 5: the Config TYPE + its box-resolution METHODS moved to
// sdk/spec (spec/config.go) and sdk/buildkit (buildkit/config_resolve.go); `Config = spec.Config`
// is now a type alias (vmshared_aliases.go-style pattern), so package main can never add another
// method to it. What STAYS here is the genuinely LoadUnified-coupled surface: LoadConfig /
// LoadConfigRaw (the load entry points) and the ResolveBox/ResolveAllBox THIN WRAPPERS that fill
// the ONE fallback (loading the project's distro:/builder: vocabulary when the caller didn't
// supply it) before delegating to buildkit's free functions — the "~35 STAY: LoadConfig/
// LoadConfigRaw + 2 fallback branches" the original scoping map identified. charly's OWN
// ResolveOpts stays a FLAT (non-embedding) struct so the ~20 existing `ResolveOpts{Field: ...}`
// composite-literal call sites across the codebase compile UNCHANGED — an embedding design would
// break every one of them (Go forbids keyed-literal initialization of a promoted field).

// ErrNoCharlyYml is the sentinel wrapped by every "no charly.yml found in the
// project dir" load error. Callers that treat an absent project as EMPTY rather
// than a hard failure (the `charly box list …` read commands — an empty project
// has zero boxes, like `ls` in an empty dir) match it with errors.Is.
var ErrNoCharlyYml = errors.New("no charly.yml found in project directory")

// noCharlyYmlErr is the ONE construction of the absent-project load error
// (config.go + format_config.go), wrapping ErrNoCharlyYml for errors.Is.
func noCharlyYmlErr(dir string) error {
	return fmt.Errorf("no charly.yml found in %s (run `charly box new project .` to scaffold one): %w", dir, ErrNoCharlyYml)
}

// Config is the charly.yml configuration projection. Relocated to sdk/spec (FLOOR-SLIM Unit 5);
// this is a type alias, not a new declaration — package main defines NO methods on it anymore.
type Config = spec.Config

// BuildFormats handles YAML unmarshal of the build: field.
// Package formats tied to the defined builders, installed in list order.
// Single string "rpm" becomes ["rpm"]. List ["pac", "aur"] stays as-is.
type BuildFormats []string

// LoadConfig reads charly.yml and returns the Config (defaults + images)
// projection. Mode purity preserved: this reads the PROJECT charly.yml only and
// never merges the per-host charly.yml overlay. Deploy-mode commands must call
// LoadBundleConfig + MergeDeployOntoMetadata explicitly.
func LoadConfig(dir string) (*Config, error) {
	return LoadConfigRaw(dir)
}

// LoadConfigRaw is an alias retained for call sites that previously
// distinguished raw-vs-merged loads. Both forms now read charly.yml via
// LoadUnified and return the Images projection.
func LoadConfigRaw(dir string) (*Config, error) {
	uf, present, err := LoadUnified(dir)
	if err != nil {
		return nil, fmt.Errorf("loading charly.yml: %w", err)
	}
	if !present {
		return nil, noCharlyYmlErr(dir)
	}
	cfg := uf.ProjectConfig()
	return cfg, nil
}

// ResolveOpts carries optional knobs for ResolveBox. Zero value is the
// default behavior used by every code path EXCEPT the explicit operational
// overrides on `charly box build/inspect/validate --include-disabled` —
// those set IncludeDisabled to bypass the `enabled: false` gate without
// requiring the operator to flip authored config.
//
// IncludeDisabledNames scopes the override: when non-empty, ONLY images in
// the set bypass the disabled check; other disabled images stay filtered.
// Used by `charly box build <name> --include-disabled` so widening the
// working set doesn't surface unrelated disabled-image dep errors (e.g.
// images with remote candies that aren't fetched yet). Empty + IncludeDisabled
// = include every disabled image (the inspect/validate behavior).
//
// Kept as ONE FLAT struct (not an embedding of buildkit.ResolveOpts) — see the file header.
type ResolveOpts struct {
	IncludeDisabled      bool            // skip the `enabled: false` check
	IncludeDisabledNames map[string]bool // when non-empty, scope IncludeDisabled to these names only
	// RequestedBoxes are the explicit build targets (`charly box build <name>`).
	// A qualified name here (e.g. `charly.arch-builder`) is pulled into the resolved
	// set even when it isn't reachable as a base/builder of a root image — so a
	// namespaced image can be an on-demand build target, not only a transitive
	// base. Bare names are ignored here (they resolve through the root loop).
	RequestedBoxes []string
	// ExtraCandyRefs are candy refs to collect IN ADDITION to the image/builder/
	// kind:local-template closure — specifically a DEPLOY's `add_candy:` candies.
	// The image-closure walk (collectBox) never reaches them (a deploy's add_candy
	// is not a base/builder/require edge of any image), so a bed that add_candy's a
	// host-side PLUGIN candy (e.g. plugin-spice for the `spice:` check verb) must
	// pass its add_candy refs here, or the plugin never enters the candy scan and
	// loadProjectPlugins can't build/connect it. Remote refs are fetched through the
	// SAME pipeline (per-entity-version arbitration + SourceDir population); a local
	// add_candy ref is already covered by ScanCandy and is a no-op here.
	//
	// NEVER read by the moved buildkit resolvers (ResolveBox/ResolveAllBox) — consumed solely by
	// charly/layers.go's ScanAllCandyWithConfigOpts — which is why it stays here rather than
	// crossing into buildkit.ResolveOpts.
	ExtraCandyRefs []string
	// InitCfg is the project init: vocabulary (W9), threaded through so
	// ScanAllCandyWithConfigOpts can run the cross-candy init-system host-completion
	// pass (the PopulateCandyInitSystem logic) BEFORE wrapping each candy into the
	// FINAL spec.CandyReader — a CandyReader is read-only from the caller's side, so
	// nothing can mutate CandyView.InitSystems after the scan returns. Every caller
	// that feeds a wire envelope another process reads for HasInit() lookups MUST set
	// this (generate.go's NewGenerator and validate_project_host.go's
	// loadProjectForResolve both do — the latter's scan feeds rp.Candies/
	// rp.CandyModels, which plugin-build's Generator consumes for real Containerfile
	// emission via EmitInitFragmentStages). A caller that leaves this nil skips the
	// pass entirely and InitSystems stays empty on every candy — correct only for a
	// caller with no init-aware consumer downstream.
	//
	// NEVER read by the moved buildkit resolvers either — same rationale as ExtraCandyRefs.
	InitCfg *InitConfig
	// DistroCfg / BuilderCfg are the project's build vocabulary (distro:/builder: —
	// the SAME triple LoadBuildConfigForBox returns alongside InitCfg), threaded
	// through so ResolveBox does not re-run LoadUnified on every call (FINAL/K5 unit
	// 6a DI refactor: config.go's ResolveBox previously called LoadBuildConfigForBox
	// itself — a REDUNDANT second full project load on top of the caller's own
	// LoadConfig, and on EVERY iteration of a multi-box loop like ResolveAllBox,
	// N reloads of the identical project-wide vocabulary). When nil, ResolveBox
	// FALLS BACK to loading them itself (byte-identical to the prior behavior) — so
	// this is purely additive: a caller that already has the triple (or loops over
	// many boxes) sets it once and skips the redundant reload; every other caller is
	// unaffected. ResolveAllBox is the primary beneficiary (loads once, threads to
	// every ResolveBox call in its loop).
	DistroCfg  *buildkit.DistroConfig
	BuilderCfg *buildkit.BuilderConfig
}

// shouldIncludeDisabled reports whether name's disabled gate should be
// bypassed under opts. Centralizes the IncludeDisabled + IncludeDisabledNames
// interaction so call sites stay simple. Stays here (not buildkit.ResolveOpts.
// ShouldIncludeDisabled) since it's charly's OWN (unmoved) ResolveOpts type — refs.go,
// resolved_project_host.go, and validate.go call this directly, unaffected by the move.
func (opts ResolveOpts) shouldIncludeDisabled(name string) bool {
	if !opts.IncludeDisabled {
		return false
	}
	if len(opts.IncludeDisabledNames) == 0 {
		return true
	}
	return opts.IncludeDisabledNames[name]
}

// toBuildkitOpts projects the box-resolution-relevant subset of opts onto buildkit.ResolveOpts —
// the ONE conversion point ResolveBox/ResolveAllBox use before delegating.
func (opts ResolveOpts) toBuildkitOpts() buildkit.ResolveOpts {
	return buildkit.ResolveOpts{
		IncludeDisabled:      opts.IncludeDisabled,
		IncludeDisabledNames: opts.IncludeDisabledNames,
		RequestedBoxes:       opts.RequestedBoxes,
		DistroCfg:            opts.DistroCfg,
		BuilderCfg:           opts.BuilderCfg,
	}
}

// fillBuildConfigFallback fills opts.DistroCfg/BuilderCfg via LoadBuildConfigForBox when the
// caller didn't already supply them — the ONE LoadUnified-coupled fallback ResolveBox and
// ResolveAllBox both need, extracted so it isn't duplicated between the two wrappers below.
func fillBuildConfigFallback(dir string, opts ResolveOpts) (ResolveOpts, error) {
	if opts.DistroCfg == nil && opts.BuilderCfg == nil {
		distroCfg, builderCfg, _, err := LoadBuildConfigForBox(dir)
		if err != nil {
			return opts, err
		}
		opts.DistroCfg, opts.BuilderCfg = distroCfg, builderCfg
	}
	return opts, nil
}

// ResolveBox resolves a single box's configuration by applying defaults. A thin wrapper: fills
// the build-config fallback (the ONE LoadUnified-coupled piece) then delegates to
// buildkit.ResolveBox, which is pure over already-loaded types. Call sites changed from
// `cfg.ResolveBox(...)` (method syntax — impossible now that Config is a type alias) to
// `ResolveBox(cfg, ...)` (free-function syntax), same name, byte-identical behavior.
func ResolveBox(cfg *Config, name string, calverTag string, dir string, opts ResolveOpts) (*buildkit.ResolvedBox, error) {
	opts, err := fillBuildConfigFallback(dir, opts)
	if err != nil {
		return nil, fmt.Errorf("image %s: %w", name, err)
	}
	return buildkit.ResolveBox(cfg, name, calverTag, dir, opts.toBuildkitOpts())
}

// ResolveAllBox resolves all enabled images in the config. opts.IncludeDisabled extends the
// working set to images marked enabled: false. Thin wrapper — see ResolveBox's doc comment.
func ResolveAllBox(cfg *Config, calverTag string, dir string, opts ResolveOpts) (map[string]*buildkit.ResolvedBox, error) {
	opts, err := fillBuildConfigFallback(dir, opts)
	if err != nil {
		return nil, fmt.Errorf("resolving build config: %w", err)
	}
	return buildkit.ResolveAllBox(cfg, calverTag, dir, opts.toBuildkitOpts())
}

// resolveIntPtr resolves a *int value, falling back to 0 when nil. A charly-side copy of the
// SHAPE of the identical helper now private to sdk/buildkit's ResolveBox (which still needs a
// 3-arg value/fallback/defaultVal form for its image->defaults->hardcoded chain) — this one serves
// build.go/build_resolve_host.go/host_build_retention.go, which are OUTSIDE this move's scope and
// (verified: every current call site) only ever pass keepImagesFallback/keepCheckRunsFallback,
// both defined as 0 (retention.go) — so both the fallback AND defaultVal parameters are dropped
// here (each was an unparam finding on the wider forms, since neither varies across any call
// site). Widen back to a defaultVal parameter if a real second default value ever emerges.
func resolveIntPtr(value *int) int {
	if value != nil {
		return *value
	}
	return 0
}
