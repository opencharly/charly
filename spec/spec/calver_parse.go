package spec

// calver_parse.go — the parsed YYYY.DDD.HHMM schema-version type + the HEAD schema version /
// migration floor + chronological comparison (RELOCATED from sdk/kit calver.go + calver_compare.go,
// #55 value extraction). Pure value/transform over the version E-envelope: it PARSES the CUE-owned
// SchemaVersion/SchemaFloor consts (this same package, generated from schema/version.cue) — there is
// no hand-maintained HEAD literal.
//
// The PARSED type is named ParsedCalVer, NOT CalVer, because this package ALREADY binds
// `CalVer = string` (scalar_aliases.go — the CUE wire scalar for `version:` fields), a DIFFERENT
// concept. sdk/kit re-exports the parsed type as `type CalVer = spec.ParsedCalVer` so every existing
// kit.CalVer / kit.ParseCalVer call site (charly core's migrate/version gate + plugin-box/clean/
// migrate) is unchanged.

import (
	"fmt"
	"strconv"
	"strings"
)

// ParsedCalVer is a parsed YYYY.DDD.HHMM calendar version. The same format that ComputeCalVer
// emits for image tags is, since the 2026-05 schema-versioning cutover, the schema-version stamp
// carried by every versioned YAML config. The declarative migration table is ordered by
// ParsedCalVer, and the load-time gate compares a file's version against LatestSchemaVersion.
type ParsedCalVer struct {
	Year int // calendar year (e.g. 2026)
	Day  int // day of year, 1-366
	HHMM int // hour*100 + minute, 0-2359
}

// ParseCalVer parses the CANONICAL CalVer string "YYYY.DDD.HHMM" — exactly a 4-digit year, a
// 3-digit zero-padded day-of-year, and a 4-digit zero-padded HHMM, separated by dots. It is
// EXTREMELY STRICT and has NO backward compatibility: every component must be the exact width, pure
// ASCII digits (no sign, no inner whitespace), within range (day 1-366, hour 0-23, minute 0-59).
// Anything else — the legacy integer "4", a non-padded "2026.45.830", an empty string, junk —
// returns ok=false. (Surrounding whitespace, a transport artifact of e.g. a `charly version`
// trailing newline, is trimmed before the format check.)
//
// A false result is exactly what the schema gate and migration runner treat as "older than every
// real CalVer", so a non-canonical config flows into `charly migrate` and is re-stamped canonical.
//
// Because the canonical form is fixed-width zero-padded, a plain alphanumeric (lexicographic) sort
// of CalVer strings is chronological (see ParsedCalVer.Less).
func ParseCalVer(s string) (ParsedCalVer, bool) {
	parts := strings.Split(strings.TrimSpace(s), ".")
	if len(parts) != 3 {
		return ParsedCalVer{}, false
	}
	if len(parts[0]) != 4 || len(parts[1]) != 3 || len(parts[2]) != 4 {
		return ParsedCalVer{}, false
	}
	if !calverAllDigits(parts[0]) || !calverAllDigits(parts[1]) || !calverAllDigits(parts[2]) {
		return ParsedCalVer{}, false
	}
	year, _ := strconv.Atoi(parts[0])
	day, _ := strconv.Atoi(parts[1])
	hhmm, _ := strconv.Atoi(parts[2])
	if year < 1970 || day < 1 || day > 366 || hhmm/100 > 23 || hhmm%100 > 59 {
		return ParsedCalVer{}, false
	}
	return ParsedCalVer{Year: year, Day: day, HHMM: hhmm}, true
}

// calverAllDigits reports whether s is non-empty and all ASCII digits. Inlined here (a 3-line
// primitive) so the CalVer parser has no dependency on a shared AllDigits helper.
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

// String renders the canonical CalVer "YYYY.DDD.HHMM" — 4-digit year, 3-digit zero-padded day,
// 4-digit zero-padded HHMM. This is the ONLY form ParseCalVer accepts, so String∘Parse is the
// identity and a plain alphanumeric sort of these strings is chronological.
func (c ParsedCalVer) String() string {
	return fmt.Sprintf("%04d.%03d.%04d", c.Year, c.Day, c.HHMM)
}

// Less reports whether c is chronologically before o. Because the canonical string form is
// fixed-width zero-padded, chronological order IS lexicographic order, so this is a plain string
// comparison.
func (c ParsedCalVer) Less(o ParsedCalVer) bool {
	return c.String() < o.String()
}

// MustCalVer parses a compile-time-constant CalVer literal, panicking on a malformed value. Used
// for the CUE-owned HEAD/floor consts (SchemaVersion / SchemaFloor), so a non-canonical literal
// that slipped past the strict #CanonCalVer CUE gate still fails fast at process start rather than
// silently mis-ordering the migration table.
func MustCalVer(s string) ParsedCalVer {
	v, ok := ParseCalVer(s)
	if !ok {
		panic("spec: invalid CalVer literal " + s)
	}
	return v
}

// latestSchemaVersion is the HEAD schema CalVer, PARSED from the CUE-owned SchemaVersion string
// const (version_gen.go, generated from schema/version.cue). Every current-format versioned file is
// stamped to it and the load-time gate requires it. Bump the HEAD by editing #SchemaVersion.
var latestSchemaVersion = MustCalVer(SchemaVersion)

// schemaFloor is the OLDEST schema CalVer `charly migrate` can migrate FROM, PARSED from the
// CUE-owned SchemaFloor string const. A config below it predates the current migration baseline.
var schemaFloor = MustCalVer(SchemaFloor)

// LatestSchemaCalVer is the HEAD schema CalVer (parsed) — every current-format versioned file is
// stamped to it and the load-time gate requires it. Named distinctly from the SchemaVersion string
// const it parses; sdk/kit re-exports it as kit.LatestSchemaVersion.
func LatestSchemaCalVer() ParsedCalVer {
	return latestSchemaVersion
}

// SchemaFloorCalVer is the oldest schema CalVer (parsed) `charly migrate` can migrate FROM. A config
// below it (or with a non-CalVer version) is unmigratable. Named distinctly from the SchemaFloor
// string const it parses; sdk/kit re-exports it as kit.SchemaFloor.
func SchemaFloorCalVer() ParsedCalVer {
	return schemaFloor
}

// CompareCalVer compares two CalVer strings numerically component-by-component, falling back to
// lexical comparison for any non-numeric component. Returns -1 if a < b, +1 if a > b, 0 if equal.
// Distinct from ParsedCalVer.Less, which requires strictly-canonical parsed CalVers; this is the
// lenient dotted-string comparator the build/tag paths use.
func CompareCalVer(a, b string) int {
	aParts := strings.Split(a, ".")
	bParts := strings.Split(b, ".")
	n := min(len(aParts), len(bParts))
	for i := range n {
		ai, aErr := strconv.Atoi(aParts[i])
		bi, bErr := strconv.Atoi(bParts[i])
		if aErr != nil || bErr != nil {
			// Fall back to lexical for this component.
			if aParts[i] < bParts[i] {
				return -1
			}
			if aParts[i] > bParts[i] {
				return 1
			}
			continue
		}
		if ai < bi {
			return -1
		}
		if ai > bi {
			return 1
		}
	}
	if len(aParts) < len(bParts) {
		return -1
	}
	if len(aParts) > len(bParts) {
		return 1
	}
	return 0
}
