package main

import (
	"fmt"
	"time"

	"github.com/opencharly/sdk/kit"
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
func ComputeCalVer() string {
	return ComputeCalVerAt(time.Now().UTC())
}

// ComputeCalVerAt computes CalVer for a specific time (for testing)
func ComputeCalVerAt(t time.Time) string {
	year := t.Year()
	dayOfYear := t.YearDay()
	// HHMM as a single integer: hour*100 + minute (08:30 -> 0830, 00:00 -> 0000).
	hhmm := t.Hour()*100 + t.Minute()

	// Canonical CalVer: 4-digit year, 3-digit zero-padded day-of-year, 4-digit
	// zero-padded HHMM. Every component is fixed-width, so a plain alphanumeric
	// (lexicographic) sort of CalVer strings is chronological.
	return fmt.Sprintf("%04d.%03d.%04d", year, dayOfYear, hhmm)
}

// CalVer is the parsed YYYY.DDD.HHMM calendar version. The
// parsed type + its parser live in sdk/kit so BOTH core (the loader
// version gate) and the candy (the migration chain) import the ONE copy; these
// zero-churn aliases keep every core call site unchanged. (The struct is kept out
// of spec because spec already binds `CalVer = string`, the CUE wire scalar.)
type CalVer = kit.CalVer

// ParseCalVer is the strict canonical "YYYY.DDD.HHMM" parser (see kit.ParseCalVer):
// a non-canonical value parses as ok=false, which the schema gate and migration
// runner treat as "older than every real CalVer".
var ParseCalVer = kit.ParseCalVer

// LatestSchemaVersion is the HEAD schema CalVer — the curated constant every
// versioned file is stamped to and the value the load-time gate requires. The
// authoritative value lives in kit (shared with the candy's migration registry,
// whose calver-schema step stamps to it); this is the in-core shim.
func LatestSchemaVersion() CalVer {
	return kit.LatestSchemaVersion()
}
