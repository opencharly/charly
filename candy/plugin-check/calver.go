package check

import (
	"fmt"
	"time"
)

// calver.go — the plugin-local CalVer formatter (P12 harness relocation).
//
// The AI-harness + bed runner tag their artifacts (image build tags, check-run
// dirs, deploy aliases, result-<calver>.yml, NOTES.md headers) with "what time is
// it NOW" in the canonical fixed-width CalVer form. The core formatter
// (charly/version.go ComputeCalVer) is package-main and not plugin-importable, so
// the plugin carries this byte-identical copy — a pure time→string formatter with
// NO host coupling.
//
// FIXED-WIDTH CONTRACT (must match charly/version.go ComputeCalVerAt EXACTLY):
//
//	YYYY.DDD.HHMM
//	  YYYY = 4-digit UTC year
//	  DDD  = 3-digit zero-padded day-of-year (1-366)
//	  HHMM = 4-digit zero-padded (hour*100 + minute) in UTC (0000-2359)
//
// Every component is fixed-width, so a plain lexicographic sort of CalVer strings
// is chronological. This is "what time is it now" (used to TAG an artifact created
// at this moment) — NEVER the identity of the charly binary.
func ComputeCalVer() string {
	return ComputeCalVerAt(time.Now().UTC())
}

// ComputeCalVerAt computes CalVer for a specific time (the fixed-width contract above).
func ComputeCalVerAt(t time.Time) string {
	year := t.Year()
	dayOfYear := t.YearDay()
	hhmm := t.Hour()*100 + t.Minute()
	return fmt.Sprintf("%04d.%03d.%04d", year, dayOfYear, hhmm)
}
