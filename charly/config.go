package main

import (
	"errors"
	"fmt"

	"github.com/opencharly/spec/spec"
)

// config.go — FLOOR-SLIM Unit 5: the Config TYPE + its box-resolution METHODS moved to
// sdk/spec (spec/config.go) and sdk/buildkit (buildkit/config_resolve.go); spec.Config
// is a spec type, so package main can never add another method to it (W0 deleted the
// former in-core `Config = spec.Config` alias too — every consumer reads spec.Config
// directly). What STAYS here is the genuinely LoadUnified-coupled surface: LoadConfig /
// LoadConfigRaw (the load entry points) and the ResolveBox/ResolveAllBox THIN WRAPPERS that fill
// the ONE fallback (loading the project's distro:/builder: vocabulary when the caller didn't
// supply it) before delegating to buildkit's free functions — the "~35 STAY: LoadConfig/
// LoadConfigRaw + 2 fallback branches" the original scoping map identified. The scan/load options
// struct (spec.ResolveOpts) + the loader validation accumulator (spec.ValidationError) live in the
// dedicated spec module (ResolveOpts relocated there in the #55 loader cascade; ValidationError in
// #55 Phase B) — charly core and the loader-consuming plugins share ONE definition (spec.ResolveOpts /
// spec.ValidationError, a FLAT non-embedding struct so every `spec.ResolveOpts{Field: ...}` call site
// stays simple).

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

// BuildFormats handles YAML unmarshal of the build: field.
// Package formats tied to the defined builders, installed in list order.
// Single string "rpm" becomes ["rpm"]. List ["pac", "aur"] stays as-is.
type BuildFormats []string

// LoadConfig reads charly.yml and returns the spec.Config (defaults + images)
// projection. Mode purity preserved: this reads the PROJECT charly.yml only and
// never merges the per-host charly.yml overlay. Deploy-mode commands must call
// LoadBundleConfig + MergeDeployOntoMetadata explicitly.
func LoadConfig(dir string) (*spec.Config, error) {
	return LoadConfigRaw(dir)
}

// LoadConfigRaw is an alias retained for call sites that previously
// distinguished raw-vs-merged loads. Both forms now read charly.yml via
// LoadUnified and return the Images projection.
func LoadConfigRaw(dir string) (*spec.Config, error) {
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
