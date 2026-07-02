package kit

// calver.go — the parsed CalVer schema-version type + the HEAD schema version /
// migration floor, shared by charly core's loader version gate and the in-core
// `charly migrate` declarative engine. The HEAD value itself is CUE-OWNED
// (charly/schema/version.cue #SchemaVersion → the generated spec.SchemaVersion
// const), which this file merely PARSES; there is no hand-maintained HEAD literal.
// The PARSED CalVer struct lives in kit (NOT spec) because spec already binds
// `CalVer = string` (the CUE wire scalar for `version:` fields) — a different
// concept, kept out of spec for that name collision. Core aliases these via
// `type CalVer = kit.CalVer` + `var ParseCalVer = kit.ParseCalVer` so every
// existing core call site is unchanged.

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/opencharly/charly/charly/spec"
)

// CalVer is a parsed YYYY.DDD.HHMM calendar version. The same format that
// ComputeCalVer (core) emits for image tags is, since the 2026-05 schema-versioning
// cutover, the schema-version stamp carried by every versioned YAML config. The
// declarative migration table is ordered by CalVer, and the load-time gate
// compares a file's CalVer against LatestSchemaVersion.
type CalVer struct {
	Year int // calendar year (e.g. 2026)
	Day  int // day of year, 1-366
	HHMM int // hour*100 + minute, 0-2359
}

// ParseCalVer parses the CANONICAL CalVer string "YYYY.DDD.HHMM" — exactly a
// 4-digit year, a 3-digit zero-padded day-of-year, and a 4-digit zero-padded
// HHMM, separated by dots. It is EXTREMELY STRICT and has NO backward
// compatibility: every component must be the exact width, pure ASCII digits
// (no sign, no inner whitespace), within range (day 1-366, hour 0-23, minute
// 0-59). Anything else — the legacy integer "4", a non-padded "2026.45.830",
// an empty string, junk — returns ok=false. (Surrounding whitespace, a
// transport artifact of e.g. a `charly version` trailing newline, is trimmed
// before the format check.)
//
// A false result is exactly what the schema gate and migration runner treat as
// "older than every real CalVer", so a non-canonical config flows into
// `charly migrate` and is re-stamped canonical — one clean migration forward.
//
// Because the canonical form is fixed-width zero-padded, a plain alphanumeric
// (lexicographic) sort of CalVer strings is chronological (see CalVer.Less).
func ParseCalVer(s string) (CalVer, bool) {
	parts := strings.Split(strings.TrimSpace(s), ".")
	if len(parts) != 3 {
		return CalVer{}, false
	}
	if len(parts[0]) != 4 || len(parts[1]) != 3 || len(parts[2]) != 4 {
		return CalVer{}, false
	}
	if !calverAllDigits(parts[0]) || !calverAllDigits(parts[1]) || !calverAllDigits(parts[2]) {
		return CalVer{}, false
	}
	year, _ := strconv.Atoi(parts[0])
	day, _ := strconv.Atoi(parts[1])
	hhmm, _ := strconv.Atoi(parts[2])
	if year < 1970 || day < 1 || day > 366 || hhmm/100 > 23 || hhmm%100 > 59 {
		return CalVer{}, false
	}
	return CalVer{Year: year, Day: day, HHMM: hhmm}, true
}

// calverAllDigits reports whether s is non-empty and all ASCII digits. Inlined
// here (a 3-line primitive) so kit's CalVer parser has no dependency on the
// vmshared AllDigits helper, which would risk an import cycle.
func calverAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// String renders the canonical CalVer "YYYY.DDD.HHMM" — 4-digit year, 3-digit
// zero-padded day, 4-digit zero-padded HHMM. This is the ONLY form ParseCalVer
// accepts, so String∘Parse is the identity and a plain alphanumeric sort of
// these strings is chronological.
func (c CalVer) String() string {
	return fmt.Sprintf("%04d.%03d.%04d", c.Year, c.Day, c.HHMM)
}

// Less reports whether c is chronologically before o. Because the canonical
// string form is fixed-width zero-padded, chronological order IS lexicographic
// order, so this is a plain string comparison.
func (c CalVer) Less(o CalVer) bool {
	return c.String() < o.String()
}

// MustCalVer parses a compile-time-constant CalVer literal, panicking on a
// malformed value. Used for the CUE-owned HEAD/floor consts (spec.SchemaVersion /
// spec.SchemaFloor), so a non-canonical literal that slipped past the strict
// #CanonCalVer CUE gate still fails fast at process start rather than silently
// mis-ordering the migration table.
func MustCalVer(s string) CalVer {
	v, ok := ParseCalVer(s)
	if !ok {
		panic("kit: invalid CalVer literal " + s)
	}
	return v
}

// latestSchemaVersion is the HEAD schema CalVer, PARSED from the CUE-owned
// spec.SchemaVersion const (charly/schema/version.cue #SchemaVersion, emitted by
// `task cue:gen`). Every current-format versioned file is stamped to it and the
// load-time gate requires it. Bump the schema HEAD by editing #SchemaVersion in
// version.cue and running `task cue:gen` — never here.
var latestSchemaVersion = MustCalVer(spec.SchemaVersion)

// schemaFloor is the OLDEST schema CalVer `charly migrate` can migrate FROM,
// PARSED from the CUE-owned spec.SchemaFloor const. A config below it predates the
// current migration baseline and is unmigratable.
var schemaFloor = MustCalVer(spec.SchemaFloor)

// LatestSchemaVersion is the HEAD schema CalVer — every current-format versioned
// file is stamped to it and the load-time gate requires it. Core exposes it via a
// thin shim of the same name; the in-core migration engine reads it directly.
func LatestSchemaVersion() CalVer {
	return latestSchemaVersion
}

// SchemaFloor is the oldest schema CalVer `charly migrate` can migrate FROM. A
// config below it (or with a non-CalVer version) is unmigratable — the engine
// refuses it with an actionable "predates the supported floor" error rather than
// blind-stamping a stale body to HEAD.
func SchemaFloor() CalVer {
	return schemaFloor
}
