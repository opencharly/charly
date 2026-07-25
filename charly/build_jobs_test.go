package main

import (
	"testing"

	"github.com/alecthomas/kong"
)

// TestBuildCmd_JobsEnvBindings verifies the Kong env bindings on the build
// parallelism flags. CHARLY_BUILD_JOBS → Jobs was missing before this cutover
// (doc/code drift the build SKILL documented but the tag lacked); CHARLY_PODMAN_JOBS
// → PodmanJobs already existed. Both are asserted here so the bindings can't
// silently regress.
func TestBuildCmd_JobsEnvBindings(t *testing.T) {
	t.Setenv("CHARLY_BUILD_JOBS", "6")
	t.Setenv("CHARLY_PODMAN_JOBS", "9")

	var cli struct {
		Build BuildCmd `cmd:""`
	}
	p, err := kong.New(&cli)
	if err != nil {
		t.Fatalf("kong.New: %v", err)
	}
	if _, err := p.Parse([]string{"build"}); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cli.Build.Jobs != 6 {
		t.Errorf("Jobs from CHARLY_BUILD_JOBS = %d, want 6", cli.Build.Jobs)
	}
	if cli.Build.PodmanJobs != 9 {
		t.Errorf("PodmanJobs from CHARLY_PODMAN_JOBS = %d, want 9", cli.Build.PodmanJobs)
	}
}

// resolvePodmanJobs's own logic (the config-driven --jobs capping) is covered by
// sdk/buildkit/build_helpers_test.go (TestResolvePodmanJobs) now that it lives there as
// buildkit.ResolvePodmanJobs (the BUILD-cone cutover).
