package main

import (
	"context"

	"github.com/opencharly/spec/spec"
)

// host_build_retention_defaults.go — the ONE thing the externalized retention engine
// (candy/plugin-clean, verb:retention) cannot compute itself: the project's
// defaults.keep_images / defaults.keep_check_runs, which need the core LoadConfig
// loader (K-wave migration inventory, not yet plugin-reachable). Reached by the
// plugin's OWN CLI (`charly clean`) and by candy/plugin-check's post-run prune hook
// — the two verb:retention callers that are NOT core and so cannot LoadConfig
// directly. Core's own callers (charly box build's pruneAfterBuild, charly box list
// tags) resolve defaults in-process instead and pass the resolved ints directly in
// the spec.RetentionRequest they send to verb:retention — this seam is unused there.
//
// Replaces the former charly/host_build_retention.go, which held the ENTIRE
// retention engine behind this HostBuild kind; the engine itself relocated to
// candy/plugin-clean (retention.go) — this file keeps ONLY the defaults-resolve
// slice, the genuinely core-coupled piece.
const retentionDefaultsBuilderKind = "retention-defaults"

func hostBuildRetentionDefaults(_ context.Context, req spec.RetentionRequest, _ buildEngineContext) (spec.RetentionReply, error) {
	cfg, err := LoadConfig(req.Dir)
	if err != nil {
		// Best-effort: the caller treats 0/0 as "retention disabled", never a hard failure.
		return spec.RetentionReply{}, nil
	}
	return spec.RetentionReply{
		KeepImages:    resolveIntPtr(cfg.Defaults.KeepImages),
		KeepCheckRuns: resolveIntPtr(cfg.Defaults.KeepCheckRuns),
	}, nil
}

var _ = func() bool {
	registerHostBuilder(retentionDefaultsBuilderKind, typedHostBuilder(retentionDefaultsBuilderKind, hostBuildRetentionDefaults))
	return true
}()

// resolveIntPtr resolves a *int retention tunable, falling back to 0 when unset. COLOCATED here from
// the deleted charly/config.go (K-wave 2 cone R1): this file is its only consumer, so a shared home
// bought nothing. sdk/buildkit keeps its own 3-arg value/fallback/default form for the box resolve's
// image->defaults->hardcoded chain; both retention callers here only ever pass a 0 fallback, so the
// extra parameters were unparam findings on the wider shape.
func resolveIntPtr(value *int) int {
	if value != nil {
		return *value
	}
	return 0
}
