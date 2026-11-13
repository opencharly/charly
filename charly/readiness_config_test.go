package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opencharly/spec/poll"
	"github.com/opencharly/spec/spec"
)

// TestReadinessProjectDir_FollowsInvocationNotCwd locks in WHICH project loadedReadiness reads.
//
// The readiness resolver reads the project's `defaults.readiness:` through LoadUnified, and it
// used to hardcode "." — the PROCESS CWD. That is the project only AFTER main chdirs into the
// resolved project dir (main.go's single-point chdir, driven by -C/--dir/CHARLY_PROJECT_DIR), but
// the ONE-SHOT sync.Once cache can be filled EARLIER: the pre-parse plugin prescan connects a
// plugin and invokes a provider, and pluginInvokeNoProgress asks loadedReadiness for its own wait
// bound. Measured live (`charly box list candies` on the umbrella project from another directory):
// the readiness-triggered LoadUnified ran at the caller's cwd, found NO project, so the project
// load — and the plugin connects that ride on it — never happened for the real project.
//
// readinessProjectDir() is the ONE place that decision lives, so this asserts it directly: the
// invocation's own resolution (CHARLY_PROJECT_DIR, -C/--dir, --repo, then cwd), never the bare cwd.
func TestReadinessProjectDir_FollowsInvocationNotCwd(t *testing.T) {
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "charly.yml"), []byte("repo: github.com/opencharly/readiness-fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The state the pre-parse prescan really runs in: a cwd with NO project at all.
	t.Chdir(t.TempDir())
	t.Setenv(spec.ProjectDirEnv, project)

	if got := readinessProjectDir(); got != project {
		t.Fatalf("readinessProjectDir() = %q, want the invocation's project %q — the bare cwd has no project", got, project)
	}
}

// With no -C/--dir, no CHARLY_PROJECT_DIR and no --repo, the invocation's project IS the cwd —
// the fallback that keeps readiness reading the project the process stands in.
func TestReadinessProjectDir_FallsBackToCwd(t *testing.T) {
	t.Setenv(spec.ProjectDirEnv, "")
	dir := t.TempDir()
	t.Chdir(dir)
	if got := readinessProjectDir(); got != dir {
		t.Fatalf("readinessProjectDir() = %q, want the cwd %q", got, dir)
	}
}

func TestReadinessConfig_ResolveDefaults(t *testing.T) {
	rr, err := poll.ResolveReadiness(nil)
	if err != nil {
		t.Fatalf("nil/default resolve must succeed: %v", err)
	}
	if rr.NoProgress != poll.ReadinessNoProgressFallback || rr.AbsoluteCap != poll.ReadinessAbsoluteCapFallback || rr.StopGrace != poll.ReadinessStopGraceFallback {
		t.Fatalf("defaults wrong: %+v", rr)
	}
	if err := rr.ValidateOrdering(); err != nil {
		t.Fatalf("default ordering must be valid: %v", err)
	}
}

func TestReadinessConfig_ParseError(t *testing.T) {
	_, err := poll.ResolveReadiness(&spec.ReadinessConfig{NoProgress: "ninety"})
	if err == nil || !strings.Contains(err.Error(), "no_progress") {
		t.Fatalf("bad duration must error, got %v", err)
	}
}

func TestReadinessConfig_OrderingRejected(t *testing.T) {
	// poll_interval_heavy (120s) > no_progress (90s default) — the env-bypass case.
	_, err := poll.ResolveReadiness(&spec.ReadinessConfig{PollIntervalHeavy: "120s"})
	if err == nil || !strings.Contains(err.Error(), "no_progress") {
		t.Fatalf("interval>no_progress must be rejected, got %v", err)
	}
	// stop_grace > absolute_cap.
	_, err = poll.ResolveReadiness(&spec.ReadinessConfig{StopGrace: "40m"})
	if err == nil || !strings.Contains(err.Error(), "absolute_cap") {
		t.Fatalf("stop_grace>absolute_cap must be rejected, got %v", err)
	}
}

func TestReadinessConfig_ValidOverride(t *testing.T) {
	rr, err := poll.ResolveReadiness(&spec.ReadinessConfig{NoProgress: "5m", AbsoluteCap: "1h"})
	if err != nil {
		t.Fatalf("valid override: %v", err)
	}
	if rr.NoProgress != 5*time.Minute || rr.AbsoluteCap != time.Hour {
		t.Fatalf("override not applied: %+v", rr)
	}
}
