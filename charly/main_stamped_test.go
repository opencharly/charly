package main

import (
	"os"
	"testing"
)

// TestShouldRefuseUnstamped is the full #74 decision — including the verbPath normalization the
// isolated checkSubcommandIsRun test could NOT catch: Kong renders the check family's passthrough as
// "check <args>", so the guard must normalize via commandPathKey (an exact "check" compare misses
// every real invocation — the live-regression bug). Refuse iff `check run` + unstamped + bypass unset.
func TestShouldRefuseUnstamped(t *testing.T) {
	savedArgs := os.Args
	savedCalVer, savedRevision, savedCommitTime, savedModified := BuildCalVer, BuildRevision, BuildCommitTime, BuildModified
	defer func() {
		os.Args = savedArgs
		BuildCalVer, BuildRevision, BuildCommitTime, BuildModified = savedCalVer, savedRevision, savedCommitTime, savedModified
	}()
	t.Setenv("CHARLY_SKIP_FRESHNESS_CHECK", "") // bypass OFF

	cases := []struct {
		name   string
		verb   string // ctx.Command() — "check <args>" for the whole check family
		args   []string
		calver string
		want   bool
	}{
		{"unstamped check run → refuse", "check <args>", []string{"charly", "check", "run", "b"}, "", true},
		{"stamped check run → allow", "check <args>", []string{"charly", "check", "run", "b"}, "2026.154.0943", false},
		{"unstamped check box → allow (scoped to run)", "check <args>", []string{"charly", "check", "box", "i"}, "", false},
		{"unstamped check live → allow", "check <args>", []string{"charly", "check", "live", "d"}, "", false},
		{"unstamped vm destroy → allow", "vm destroy", []string{"charly", "vm", "destroy", "x"}, "", false},
	}
	for _, c := range cases {
		os.Args = c.args
		BuildCalVer = c.calver
		if c.calver == "" {
			BuildRevision, BuildCommitTime, BuildModified = "", "", ""
		} else {
			BuildRevision = "0123456789abcdef"
			BuildCommitTime = "2026-06-03T09:43:00+00:00"
			BuildModified = "false"
		}
		if got := shouldRefuseUnstamped(c.verb); got != c.want {
			t.Errorf("%s: shouldRefuseUnstamped(%q) = %v, want %v", c.name, c.verb, got, c.want)
		}
	}

	// The bypass short-circuits even the refuse case.
	os.Args = []string{"charly", "check", "run", "b"}
	BuildCalVer, BuildRevision, BuildCommitTime, BuildModified = "", "", "", ""
	t.Setenv("CHARLY_SKIP_FRESHNESS_CHECK", "1")
	if shouldRefuseUnstamped("check <args>") {
		t.Error("CHARLY_SKIP_FRESHNESS_CHECK=1 must disable the refusal")
	}
}

// TestCheckSubcommandIsRun locks the os.Args passthrough-subcommand recovery (run vs box/live).
func TestCheckSubcommandIsRun(t *testing.T) {
	saved := os.Args
	defer func() { os.Args = saved }()

	cases := []struct {
		name string
		args []string
		want bool
	}{
		{"check run", []string{"charly", "check", "run", "mybed"}, true},
		{"check run with global -C before", []string{"charly", "-C", "/proj", "check", "run", "mybed"}, true},
		{"check box", []string{"charly", "check", "box", "img"}, false},
		{"check live", []string{"charly", "check", "live", "dep"}, false},
		{"bare check", []string{"charly", "check"}, false},
		{"unrelated verb", []string{"charly", "vm", "destroy", "x"}, false},
	}
	for _, c := range cases {
		os.Args = c.args
		if got := checkSubcommandIsRun(); got != c.want {
			t.Errorf("%s: checkSubcommandIsRun(%v) = %v, want %v", c.name, c.args, got, c.want)
		}
	}
}

// TestVersionCmd_UnstampedReturnsError proves `charly version` exits non-zero (returns an error) on an
// UNSTAMPED binary so scripts can gate on it, and stays clean (nil) when stamped (#74).
func TestVersionCmd_UnstampedReturnsError(t *testing.T) {
	savedCalVer, savedRevision, savedCommitTime, savedModified := BuildCalVer, BuildRevision, BuildCommitTime, BuildModified
	defer func() {
		BuildCalVer, BuildRevision, BuildCommitTime, BuildModified = savedCalVer, savedRevision, savedCommitTime, savedModified
	}()

	BuildCalVer = "2026.154.0943"
	BuildRevision = "0123456789abcdef"
	BuildCommitTime = "2026-06-03T09:43:00+00:00"
	BuildModified = "false"
	if err := (&VersionCmd{}).Run(); err != nil {
		t.Errorf("provenance-complete VersionCmd.Run() = %v, want nil", err)
	}
	BuildCalVer, BuildRevision, BuildCommitTime, BuildModified = "", "", "", ""
	if err := (&VersionCmd{}).Run(); err == nil {
		t.Error("incomplete VersionCmd.Run() = nil, want a non-nil error (scripts gate on the non-zero exit)")
	}
}
