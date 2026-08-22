package spec

import (
	"fmt"
	"time"
)

// calver.go — the canonical CalVer build-tag computation. #55 import-purity: relocated from
// sdk/buildkit (the build-engine kit) DOWN to spec (the wire/value leaf) so charly core reaches
// the ONE source through its spec+proto-only import surface, never sdk/buildkit. It is a pure
// D-value computation over the wall clock — no loader/registry coupling — so spec is its home;
// sdk/buildkit keeps a thin var-forwarder for its plugin callers (candy/plugin-build etc.).

// ComputeCalVer returns the canonical build tag for the current UTC instant.
func ComputeCalVer() string {
	return ComputeCalVerAt(time.Now().UTC())
}

// ComputeCalVerAt formats t as the canonical CalVer: 4-digit year, 3-digit zero-padded day-of-year,
// 4-digit zero-padded HHMM. Every component is fixed-width, so a plain lexicographic sort of CalVer
// strings is chronological.
func ComputeCalVerAt(t time.Time) string {
	year := t.Year()
	dayOfYear := t.YearDay()
	hhmm := t.Hour()*100 + t.Minute()
	return fmt.Sprintf("%04d.%03d.%04d", year, dayOfYear, hhmm)
}
