package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/opencharly/spec/spec"
)

// git_layer_behaviors_test.go — B12 coverage for the three NEW behaviors this cutover
// introduces. Each test fails if its behavior is removed or returns the wrong value;
// the kernel-manifest row and the reverse-channel whitelist entry are registration
// gates, not proofs that these paths compute anything correct.

// ---------------------------------------------------------------------------
// builtinOnlyInvocation — decides whether the prescan skips its remote fetch.
// A mis-firing skip is silent: a builtin would fetch 30+ repos (slow but correct),
// or worse, a non-builtin would SKIP and lose its external command words.
// ---------------------------------------------------------------------------

func TestBuiltinOnlyInvocation(t *testing.T) {
	saved := os.Args
	t.Cleanup(func() { os.Args = saved })

	cases := []struct {
		name string
		args []string
		want bool
	}{
		{"bare charly is help", []string{"charly"}, true},
		{"version", []string{"charly", "version"}, true},
		{"help", []string{"charly", "help"}, true},
		{"--help", []string{"charly", "--help"}, true},
		{"-h", []string{"charly", "-h"}, true},
		{"settings", []string{"charly", "settings"}, true},
		{"ssh", []string{"charly", "ssh"}, true},
		// The discriminating half: these MUST NOT skip, or they lose external words.
		{"build needs external words", []string{"charly", "build"}, false},
		{"check needs external words", []string{"charly", "check", "run", "x"}, false},
		{"config needs external words", []string{"charly", "config"}, false},
		{"start needs external words", []string{"charly", "start"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			os.Args = tc.args
			if got := builtinOnlyInvocation(); got != tc.want {
				t.Errorf("builtinOnlyInvocation(%v) = %v, want %v", tc.args, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// hostBuildRetentionDefaults — reads defaults.keep_images / keep_check_runs
// WITHOUT walking the project. Wrong values here silently over- or under-prune.
// ---------------------------------------------------------------------------

func writeRetentionProject(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, spec.UnifiedFileName), []byte(body), 0o600); err != nil {
		t.Fatalf("write charly.yml: %v", err)
	}
	return dir
}

func TestHostBuildRetentionDefaults_ReadsBothFields(t *testing.T) {
	dir := writeRetentionProject(t, `
version: "2026.249.2125"
defaults:
    keep_images: 7
    keep_check_runs: 3
`)
	got, err := hostBuildRetentionDefaults(context.Background(), spec.RetentionRequest{Dir: dir}, buildEngineContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.KeepImages != 7 {
		t.Errorf("KeepImages = %d, want 7", got.KeepImages)
	}
	if got.KeepCheckRuns != 3 {
		t.Errorf("KeepCheckRuns = %d, want 3", got.KeepCheckRuns)
	}
}

// An explicit 0 must be preserved as 0, and a MISSING field must also be 0 --
// the pointer fields exist so the seam can tell them apart internally, but both
// legitimately mean "retention disabled" to the caller.
func TestHostBuildRetentionDefaults_PartialAndZero(t *testing.T) {
	dir := writeRetentionProject(t, `
defaults:
    keep_images: 0
`)
	got, err := hostBuildRetentionDefaults(context.Background(), spec.RetentionRequest{Dir: dir}, buildEngineContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.KeepImages != 0 || got.KeepCheckRuns != 0 {
		t.Errorf("got %+v, want both 0", got)
	}
}

// Absent and unparseable projects degrade to 0/0 with a NIL error -- the
// documented best-effort contract inherited from the deleted seam.
func TestHostBuildRetentionDefaults_DegradesQuietly(t *testing.T) {
	t.Run("absent charly.yml", func(t *testing.T) {
		got, err := hostBuildRetentionDefaults(context.Background(), spec.RetentionRequest{Dir: t.TempDir()}, buildEngineContext{})
		if err != nil {
			t.Fatalf("absent project must not error, got %v", err)
		}
		if got.KeepImages != 0 || got.KeepCheckRuns != 0 {
			t.Errorf("got %+v, want zero reply", got)
		}
	})
	t.Run("unparseable charly.yml", func(t *testing.T) {
		dir := writeRetentionProject(t, "defaults: [this is not a mapping\n")
		got, err := hostBuildRetentionDefaults(context.Background(), spec.RetentionRequest{Dir: dir}, buildEngineContext{})
		if err != nil {
			t.Fatalf("unparseable project must not error, got %v", err)
		}
		if got.KeepImages != 0 || got.KeepCheckRuns != 0 {
			t.Errorf("got %+v, want zero reply", got)
		}
	})
}

// ---------------------------------------------------------------------------
// gitClient — the process-wide singleton the whole point of this cutover is to
// route through. A per-call client would silently discard the cache and restore
// the 168s status timing this PR exists to fix.
// ---------------------------------------------------------------------------

func TestGitClientIsSingleton(t *testing.T) {
	a := gitClient()
	b := gitClient()
	if a == nil {
		t.Fatal("gitClient() returned nil")
	}
	if a != b {
		t.Error("gitClient() returned distinct instances; the cache would be discarded per call")
	}
}
