package main

import (
	"testing"

	"github.com/opencharly/sdk/kit"
)

// TestShellSingleQuoters_CanonicalPOSIX asserts EVERY package-main POSIX single-quoter — plus the
// canonical kit.ShellQuote they all fold onto — emits the identical canonical single-quoted form for
// a corpus of adversarial inputs (shell metachars, embedded quotes, command-substitution, newlines).
// It is the equivalence + regression guard for the shell-quoter consolidation (FU-13 deployShellQuote,
// FU-14 shellSingleQuoteSSH + wl.shellQuote): the corpus passes against each helper's ORIGINAL impl
// AND against the kit.ShellQuote alias, proving they are behaviorally identical (inside '...' the ONLY
// special char is ', escaped '\” by all; everything else — $, `, ;, ", \n — is literal). These are
// the quoters for SSH/podman/dbus/wayland command construction, so equivalence is security-relevant.
func TestShellSingleQuoters_CanonicalPOSIX(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", "''"},
		{"abc", "'abc'"},
		{"a'b", `'a'\''b'`},
		{"'", `''\'''`},
		{"a'b'c", `'a'\''b'\''c'`},
		{"$(rm -rf /)", "'$(rm -rf /)'"},
		{"`whoami`", "'`whoami`'"},
		{"a b; c && d", "'a b; c && d'"},
		{`a"b`, `'a"b'`},
		{"a\nb", "'a\nb'"},
	}
	quoters := map[string]func(string) string{
		"deployShellQuote":    deployShellQuote,
		"shellSingleQuoteSSH": shellSingleQuoteSSH,
		"shellQuote":          shellQuote,
		"kit.ShellQuote":      kit.ShellQuote,
	}
	for name, q := range quoters {
		for _, c := range cases {
			if got := q(c.in); got != c.want {
				t.Errorf("%s(%q) = %q, want %q", name, c.in, got, c.want)
			}
		}
	}
}
