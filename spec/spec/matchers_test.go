package spec

import (
	"strings"
	"testing"
)

// Sync guard: every operator allowed by #MatchOpMap (the CUE matcher-operator
// authority in _common.cue) must be implemented by matchOne — a new allow-listed
// op without a runner branch would crash at runtime. Keep this list in sync with
// #MatchOpMap.
//
// Relocated from github.com/opencharly/sdk/matchers_test.go (#55 import-purity):
// matchOne is private to this package, so its tests live HERE alongside it (a
// private symbol cannot be reached cross-package).
func TestMatcher_AllowlistRunnerSync(t *testing.T) {
	matcherOps := []string{
		"equals", "not_equals", "contains", "not_contains",
		"matches", "not_matches", "lt", "le", "gt", "ge",
	}
	for _, op := range matcherOps {
		err := matchOne("x", Matcher{Op: op, Value: "x"})
		// Either a clean result or a domain-specific error is fine; an
		// "unsupported matcher op" error means matchOne is missing a case.
		if err != nil && strings.Contains(err.Error(), "unsupported matcher op") {
			t.Errorf("#MatchOpMap allows op %q but runner has no implementation", op)
		}
	}
}

// Verifies every matcher operator has a runner path — guards against the earlier
// regression where lt/le/gt/ge and not_equals were declared valid by the
// validator but crashed at runtime.
//
// Relocated from github.com/opencharly/sdk/matchers_test.go (#55 import-purity):
// exercises the private matchOne, so it lives in this package.
func TestMatcher_AllOperators(t *testing.T) {
	cases := []struct {
		name    string
		value   string
		matcher Matcher
		wantErr bool
	}{
		{"equals pass", "hello", Matcher{Op: "equals", Value: "hello"}, false},
		{"equals fail", "hello", Matcher{Op: "equals", Value: "world"}, true},
		{"not_equals pass", "hello", Matcher{Op: "not_equals", Value: "world"}, false},
		{"not_equals fail", "hello", Matcher{Op: "not_equals", Value: "hello"}, true},
		{"contains pass", "hello world", Matcher{Op: "contains", Value: "world"}, false},
		{"contains fail", "hello world", Matcher{Op: "contains", Value: "xyz"}, true},
		{"not_contains pass", "hello", Matcher{Op: "not_contains", Value: "xyz"}, false},
		{"not_contains fail", "hello", Matcher{Op: "not_contains", Value: "ell"}, true},
		{"matches pass", "abc123", Matcher{Op: "matches", Value: `\d+`}, false},
		{"matches fail", "abc", Matcher{Op: "matches", Value: `\d+`}, true},
		{"not_matches pass", "abc", Matcher{Op: "not_matches", Value: `\d+`}, false},
		{"not_matches fail", "abc123", Matcher{Op: "not_matches", Value: `\d+`}, true},
		{"lt pass", "5", Matcher{Op: "lt", Value: "10"}, false},
		{"lt fail", "10", Matcher{Op: "lt", Value: "5"}, true},
		{"le pass equal", "10", Matcher{Op: "le", Value: "10"}, false},
		{"gt pass", "10", Matcher{Op: "gt", Value: "5"}, false},
		{"ge pass equal", "5", Matcher{Op: "ge", Value: "5"}, false},
		{"lt non-numeric observed", "x", Matcher{Op: "lt", Value: "10"}, true},
		{"lt non-numeric want", "5", Matcher{Op: "lt", Value: "nope"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := matchOne(tc.value, tc.matcher)
			if tc.wantErr && err == nil {
				t.Errorf("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}
