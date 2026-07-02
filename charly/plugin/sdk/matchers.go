package sdk

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/opencharly/charly/charly/spec"
)

// ---------------------------------------------------------------------------
// Matcher evaluation
//
// These goss-style matcher helpers are the SINGLE matcher implementation,
// shared by charly's core check runner AND out-of-tree verb plugins (which can
// import only the SDK). One implementation, two consumers — no duplication (R3).
// ---------------------------------------------------------------------------

// Matcher is re-exported from charly/spec so an out-of-tree plugin reaches the
// matcher value type through the SDK alone (an external plugin imports no other
// charly package).
type Matcher = spec.Matcher

// MatchAll returns nil if every matcher succeeds against the value. The first
// failure wins (reports the specific unmet expectation).
//
// Takes []Matcher rather than MatcherList so callers can pass any named slice
// type whose underlying element is Matcher (e.g. ContainsList) without an
// explicit conversion at every call site.
//
// Shared by the core check runner and out-of-tree verb plugins.
func MatchAll(value string, matchers []Matcher) error {
	for _, m := range matchers {
		if err := matchOne(value, m); err != nil {
			return err
		}
	}
	return nil
}

// matchOne evaluates a single matcher. The operator set here must stay in
// lockstep with #MatchOpMap (the CUE matcher-operator authority in _common.cue)
// — if the schema accepts an op, the runner must handle it.
func matchOne(value string, m Matcher) error {
	switch m.Op {
	case "equals":
		want := MatchValueString(m.Value)
		if strings.TrimRight(value, "\r\n") != want {
			return fmt.Errorf("expected exactly %q", want)
		}
	case "not_equals":
		want := MatchValueString(m.Value)
		if strings.TrimRight(value, "\r\n") == want {
			return fmt.Errorf("expected NOT to equal %q", want)
		}
	case "contains":
		for _, want := range MatchValueStrings(m.Value) {
			if !strings.Contains(value, want) {
				return fmt.Errorf("expected to contain %q", want)
			}
		}
	case "not_contains":
		for _, want := range MatchValueStrings(m.Value) {
			if strings.Contains(value, want) {
				return fmt.Errorf("expected NOT to contain %q", want)
			}
		}
	case "matches":
		re, err := regexp.Compile(MatchValueString(m.Value))
		if err != nil {
			return fmt.Errorf("bad regex %v: %w", m.Value, err)
		}
		if !re.MatchString(value) {
			return fmt.Errorf("expected to match /%s/", re.String())
		}
	case "not_matches":
		re, err := regexp.Compile(MatchValueString(m.Value))
		if err != nil {
			return fmt.Errorf("bad regex %v: %w", m.Value, err)
		}
		if re.MatchString(value) {
			return fmt.Errorf("expected NOT to match /%s/", re.String())
		}
	case "lt", "le", "gt", "ge":
		return matchNumeric(value, m)
	default:
		return fmt.Errorf("unsupported matcher op %q", m.Op)
	}
	return nil
}

// matchNumeric compares both sides as float64. Used for HTTP status codes,
// kernel-param integers, port counts — anywhere an ordering-aware matcher
// makes sense. String values with leading/trailing whitespace (like
// `sysctl -n` output) are trimmed before parsing.
func matchNumeric(value string, m Matcher) error {
	wantStr := MatchValueString(m.Value)
	want, err := strconv.ParseFloat(strings.TrimSpace(wantStr), 64)
	if err != nil {
		return fmt.Errorf("%s: operand %q not numeric: %w", m.Op, wantStr, err)
	}
	got, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil {
		return fmt.Errorf("%s: observed %q not numeric: %w", m.Op, value, err)
	}
	var ok bool
	switch m.Op {
	case "lt":
		ok = got < want
	case "le":
		ok = got <= want
	case "gt":
		ok = got > want
	case "ge":
		ok = got >= want
	}
	if !ok {
		return fmt.Errorf("expected %s %v (got %v)", m.Op, want, got)
	}
	return nil
}

// MatchValueString coerces a matcher's stored Value (any) to a string. For
// numeric types it renders canonically; for everything else it falls back
// to fmt.Sprint.
//
// Shared by the core check runner and out-of-tree verb plugins.
func MatchValueString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	case nil:
		return ""
	}
	return fmt.Sprint(v)
}

// MatchValueStrings handles list-valued matchers like {contains: [a, b]}.
// A scalar value becomes a singleton list.
//
// Shared by the core check runner and out-of-tree verb plugins.
func MatchValueStrings(v any) []string {
	if list, ok := v.([]any); ok {
		out := make([]string, 0, len(list))
		for _, e := range list {
			out = append(out, MatchValueString(e))
		}
		return out
	}
	return []string{MatchValueString(v)}
}
