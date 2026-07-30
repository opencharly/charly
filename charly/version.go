package main

import (
	"time"

	"github.com/opencharly/spec/spec"
)

// BuildCalVer is the CalVer build identity of THIS binary, injected at compile
// time via `-ldflags "-X main.BuildCalVer=<calver>"` (see taskfiles/Build.yml +
// pkg/arch/PKGBUILD, both of which derive it from the git commit date through
// pkg/arch/calver.sh — the same value `pacman -Q opencharly-git` reports). Empty
// for an unstamped build (`go build` / `go test` without the ldflag).
//
// This is the binary's TRUE identity — frozen at build time — as opposed to
// ComputeCalVer() below, which is a wall-clock readout of the current moment.
// `charly version` reports BuildCalVer, never the clock: two different binaries must
// never claim the same version, and a newer build must sort higher so a CalVer
// comparison is a RELIABLE freshness signal (a content checksum tells you
// "different" but never "newer" — useless for deciding which charly to keep).
var BuildCalVer string

// CharlyVersion returns the CalVer identity of this `charly` binary. It is the stamped
// BuildCalVer when present; otherwise "unknown" (an unstamped dev/test build —
// ParseCalVer rejects it, so freshness comparisons treat it as older than every
// real CalVer). It NEVER falls back to the wall clock: the clock identifies the
// moment of invocation, not the binary, and that conflation is exactly the
// defect this replaces.
func CharlyVersion() string {
	if BuildCalVer != "" {
		return BuildCalVer
	}
	return "unknown"
}

// ComputeCalVer computes a CalVer version in the format YYYY.DDD.HHMM
// where:
//   - YYYY = year (e.g., 2026)
//   - DDD  = day of year (1-366)
//   - HHMM = hour and minute in UTC (0000-2359)
//
// This produces valid semver: all three components are non-negative integers.
// Versions sort correctly both lexically and numerically.
//
// NB: this is "what time is it NOW", used to TAG an artifact created at this
// moment (image build tag, check-run dir, deploy alias). It is NOT the identity
// of the charly binary — that is CharlyVersion()/BuildCalVer. Never use ComputeCalVer()
// to report the running binary's version.
// ComputeCalVer / ComputeCalVerAt delegate to spec (#55 import-purity): the computation lives in
// spec (spec.ComputeCalVer) so candy/plugin-build's plugin-side RESOLVE stamps the SAME tag when the
// host leaves req.Tag empty (R3, one source) AND charly core reaches it over its spec+proto-only
// import surface. These charly-side names are retained for the ~14 host call sites.
func ComputeCalVer() string {
	return spec.ComputeCalVer()
}

// ComputeCalVerAt computes CalVer for a specific time (for testing).
func ComputeCalVerAt(t time.Time) string {
	return spec.ComputeCalVerAt(t)
}

// CalVer is the parsed YYYY.DDD.HHMM calendar version. The parsed type + its parser live in spec
// (spec.ParsedCalVer, #55 value extraction) so BOTH core (the loader version gate) and the candy
// (the migration chain) reference the ONE copy; these zero-churn aliases keep every core call site
// unchanged. (The struct is named ParsedCalVer in spec because spec already binds `CalVer = string`,
// the CUE wire scalar.)
type CalVer = spec.ParsedCalVer

// ParseCalVer is the strict canonical "YYYY.DDD.HHMM" parser (see spec.ParseCalVer):
// a non-canonical value parses as ok=false, which the schema gate and migration
// runner treat as "older than every real CalVer".
var ParseCalVer = spec.ParseCalVer

// LatestSchemaVersion is the HEAD schema CalVer — the curated constant every
// versioned file is stamped to and the value the load-time gate requires. The
// authoritative value lives in spec (shared with the candy's migration registry,
// whose calver-schema step stamps to it); this is the in-core shim.
func LatestSchemaVersion() CalVer {
	return spec.LatestSchemaCalVer()
}
