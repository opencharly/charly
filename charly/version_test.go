package main

import (
	"sort"
	"testing"
	"time"
)

func TestCharlyVersion(t *testing.T) {
	// CharlyVersion reports the binary's STAMPED identity (BuildCalVer), never the
	// wall clock. Save/restore the package var around the test.
	saved := BuildCalVer
	defer func() { BuildCalVer = saved }()

	BuildCalVer = "2026.154.0943"
	if got := CharlyVersion(); got != "2026.154.0943" {
		t.Errorf("stamped CharlyVersion() = %q, want 2026.154.0943", got)
	}

	BuildCalVer = ""
	if got := CharlyVersion(); got != "unknown" {
		t.Errorf("unstamped CharlyVersion() = %q, want %q", got, "unknown")
	}
	// "unknown" must be rejected by ParseCalVer so freshness treats it as oldest.
	if _, ok := ParseCalVer(CharlyVersion()); ok {
		t.Errorf("ParseCalVer(%q) parsed ok; an unstamped build must sort as oldest", CharlyVersion())
	}
}

func TestComputeCalVerAt(t *testing.T) {
	tests := []struct {
		name     string
		time     time.Time
		expected string
	}{
		{
			name:     "morning build on Feb 14, 2026 (day 45)",
			time:     time.Date(2026, 2, 14, 8, 30, 0, 0, time.UTC),
			expected: "2026.045.0830",
		},
		{
			name:     "afternoon build on Feb 14, 2026",
			time:     time.Date(2026, 2, 14, 14, 15, 0, 0, time.UTC),
			expected: "2026.045.1415",
		},
		{
			name:     "evening build on Feb 14, 2026",
			time:     time.Date(2026, 2, 14, 22, 1, 0, 0, time.UTC),
			expected: "2026.045.2201",
		},
		{
			name:     "midnight build",
			time:     time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			expected: "2026.001.0000",
		},
		{
			name:     "end of year",
			time:     time.Date(2026, 12, 31, 23, 59, 0, 0, time.UTC),
			expected: "2026.365.2359",
		},
		{
			name:     "leap year end of year",
			time:     time.Date(2024, 12, 31, 12, 0, 0, 0, time.UTC),
			expected: "2024.366.1200",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ComputeCalVerAt(tt.time)
			if got != tt.expected {
				t.Errorf("ComputeCalVerAt(%v) = %q, want %q", tt.time, got, tt.expected)
			}
		})
	}
}

func TestComputeCalVer(t *testing.T) {
	// Just verify it returns something in the right format
	version := ComputeCalVer()
	if version == "" {
		t.Error("ComputeCalVer() returned empty string")
	}
	// Should contain two dots (three parts)
	dots := 0
	for _, c := range version {
		if c == '.' {
			dots++
		}
	}
	if dots != 2 {
		t.Errorf("ComputeCalVer() = %q, expected format YYYY.DDD.HHMM with 2 dots", version)
	}
}

func TestParseCalVer(t *testing.T) {
	cases := []struct {
		in   string
		ok   bool
		year int
		day  int
		hhmm int
	}{
		{"2026.141.1530", true, 2026, 141, 1530},
		{"2026.001.0000", true, 2026, 1, 0},
		{"  2026.366.2359  ", true, 2026, 366, 2359}, // trimmed
		{"4", false, 0, 0, 0},                        // legacy integer schema version
		{"", false, 0, 0, 0},
		{"2026.141", false, 0, 0, 0},         // too few parts
		{"2026.141.0015.30", false, 0, 0, 0}, // too many parts
		{"x.y.z", false, 0, 0, 0},
		{"2026.000.0000", false, 0, 0, 0}, // day < 1
		{"2026.367.0000", false, 0, 0, 0}, // day > 366
		{"2026.141.2400", false, 0, 0, 0}, // hour > 23
		{"1969.001.0000", false, 0, 0, 0}, // year < 1970
		// EXTREMELY STRICT — non-canonical widths/forms are rejected (no back-compat):
		{"2026.45.0830", false, 0, 0, 0},  // day not 3-digit
		{"2026.1.0000", false, 0, 0, 0},   // day not 3-digit
		{"2026.001.830", false, 0, 0, 0},  // hhmm not 4-digit
		{"2026.141.15", false, 0, 0, 0},   // hhmm not 4-digit
		{"226.001.0000", false, 0, 0, 0},  // year not 4-digit
		{"2026.141.2360", false, 0, 0, 0}, // minute > 59
		{"+026.001.0000", false, 0, 0, 0}, // non-digit component (sign)
	}
	for _, c := range cases {
		got, ok := ParseCalVer(c.in)
		if ok != c.ok {
			t.Errorf("ParseCalVer(%q) ok = %v, want %v", c.in, ok, c.ok)
			continue
		}
		if !ok {
			continue
		}
		if got.Year != c.year || got.Day != c.day || got.HHMM != c.hhmm {
			t.Errorf("ParseCalVer(%q) = %+v, want {%d %d %d}", c.in, got, c.year, c.day, c.hhmm)
		}
	}
}

func TestCalVerRoundTrip(t *testing.T) {
	for _, s := range []string{"2026.141.1530", "2026.001.0000", "2026.366.2359"} {
		v, ok := ParseCalVer(s)
		if !ok {
			t.Fatalf("ParseCalVer(%q) failed", s)
		}
		if v.String() != s {
			t.Errorf("round-trip %q -> %q", s, v.String())
		}
	}
}

// TestCalVerAlphanumericSort is the load-bearing guarantee: because every
// canonical CalVer is fixed-width zero-padded, a plain alphanumeric
// (lexicographic) sort of the strings yields chronological order. This is what
// lets any consumer sort CalVers with sort.Strings and trust the result.
func TestCalVerAlphanumericSort(t *testing.T) {
	// Chronological order, by hand.
	chrono := []string{
		"2024.001.0000",
		"2026.005.0002",
		"2026.045.0830",
		"2026.045.0831",
		"2026.112.0522",
		"2026.161.2303",
		"2026.164.0004",
		"2026.366.2359",
	}
	// A shuffled copy, sorted PURELY alphanumerically, must equal chrono.
	shuffled := []string{
		"2026.164.0004", "2024.001.0000", "2026.045.0831", "2026.112.0522",
		"2026.366.2359", "2026.005.0002", "2026.161.2303", "2026.045.0830",
	}
	sort.Strings(shuffled)
	for i := range chrono {
		if shuffled[i] != chrono[i] {
			t.Fatalf("alphanumeric sort != chronological at %d: got %q want %q\n got:  %v\n want: %v",
				i, shuffled[i], chrono[i], shuffled, chrono)
		}
	}
	// CalVer.Less must agree with the alphanumeric order, and every canonical
	// string must parse.
	for i := 0; i+1 < len(chrono); i++ {
		a, okA := ParseCalVer(chrono[i])
		b, okB := ParseCalVer(chrono[i+1])
		if !okA || !okB {
			t.Fatalf("canonical CalVer rejected: %q ok=%v, %q ok=%v", chrono[i], okA, chrono[i+1], okB)
		}
		if !a.Less(b) {
			t.Errorf("(%s).Less(%s) = false, want true", chrono[i], chrono[i+1])
		}
	}
}

func TestCalVerLess(t *testing.T) {
	cases := []struct {
		a, b string
		less bool
	}{
		{"2026.112.0522", "2026.114.1558", true},  // earlier day
		{"2026.114.1558", "2026.114.2207", true},  // same day, earlier time
		{"2026.141.1326", "2026.141.1530", true},  // drop-kdbx < calver-schema (HEAD)
		{"2026.141.1530", "2026.141.1530", false}, // equal is not less
		{"2026.141.1530", "2026.141.1326", false}, // reverse
		{"2025.366.2359", "2026.001.0000", true},  // year boundary
	}
	for _, c := range cases {
		a, _ := ParseCalVer(c.a)
		b, _ := ParseCalVer(c.b)
		if got := a.Less(b); got != c.less {
			t.Errorf("(%s).Less(%s) = %v, want %v", c.a, c.b, got, c.less)
		}
	}
}
