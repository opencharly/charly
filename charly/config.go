package main

import (
	"errors"
	"fmt"

	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/spec/spec"
)

// config.go — FLOOR-SLIM Unit 5: the Config TYPE + its box-resolution METHODS moved to
// sdk/spec (spec/config.go) and sdk/buildkit (buildkit/config_resolve.go); `Config = spec.Config`
// is now a spec type alias, so package main can never add another
// method to it. What STAYS here is the genuinely LoadUnified-coupled surface: LoadConfig /
// LoadConfigRaw (the load entry points) and the ResolveBox/ResolveAllBox THIN WRAPPERS that fill
// the ONE fallback (loading the project's distro:/builder: vocabulary when the caller didn't
// supply it) before delegating to buildkit's free functions — the "~35 STAY: LoadConfig/
// LoadConfigRaw + 2 fallback branches" the original scoping map identified. The scan/load options
// struct (loaderkit.ResolveOpts) + the loader validation accumulator (spec.ValidationError) moved to sdk/loaderkit
// (resolve_opts.go) in the #118 Cluster-A loader-projection keystone — charly core and the
// loader-consuming plugins share ONE definition (loaderkit.ResolveOpts / spec.ValidationError,
// a FLAT non-embedding struct so every `loaderkit.ResolveOpts{Field: ...}` call site stays simple).

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

// buildkitOptsWithVocab projects a loaderkit.ResolveOpts onto buildkit.ResolveOpts, loading the project
// build vocabulary (distro:/builder:) when the caller did not already supply it. It is the ONE place
// the former ResolveBox/ResolveAllBox wrappers' fillBuildConfigFallback + toBuildkitOpts logic lives
// (K3 U7: the build-engine RESOLVE moved to candy/plugin-build, so charly-side callers now reach the
// PURE buildkit.ResolveBox / buildkit.ResolveAllBox DIRECTLY over this opts projection — the config.go
// ResolveBox/ResolveAllBox free-function wrappers are DELETED). BYTE-EQUIVALENT to the former fallback:
// a caller that already has DistroCfg/BuilderCfg skips the reload; every other caller gets the SAME
// vocabulary LoadBuildConfigForBox loads for the same dir — the masked-regression this preserves.
func buildkitOptsWithVocab(dir string, opts loaderkit.ResolveOpts) (buildkit.ResolveOpts, error) {
	if opts.DistroCfg == nil && opts.BuilderCfg == nil {
		distroCfg, builderCfg, _, err := LoadBuildConfigForBox(dir)
		if err != nil {
			return buildkit.ResolveOpts{}, err
		}
		opts.DistroCfg, opts.BuilderCfg = distroCfg, builderCfg
	}
	return buildkit.ResolveOpts{
		IncludeDisabled:      opts.IncludeDisabled,
		IncludeDisabledNames: opts.IncludeDisabledNames,
		RequestedBoxes:       opts.RequestedBoxes,
		DistroCfg:            opts.DistroCfg,
		BuilderCfg:           opts.BuilderCfg,
	}, nil
}

// resolveIntPtr resolves a *int value, falling back to 0 when nil. A charly-side copy of the
// SHAPE of the identical helper now private to sdk/buildkit's ResolveBox (which still needs a
// 3-arg value/fallback/defaultVal form for its image->defaults->hardcoded chain) — this one serves
// build.go/host_build_retention.go, which are OUTSIDE this move's scope and
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
