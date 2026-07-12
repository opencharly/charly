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

// TestResolvePodmanJobs verifies the config-driven --jobs capping logic. The
// cap is sourced from defaults.podman_jobs_cap (passed as jobsCap); a jobsCap
// of 0 falls back to podmanJobsCapFallback. The helper must:
//   - honor an explicit override (>0) verbatim, ignoring cap + ncpu
//   - when no override: return min(numCPU(), cap)
//   - treat jobsCap < 1 as podmanJobsCapFallback
func TestResolvePodmanJobs(t *testing.T) {
	origNumCPU := numCPU
	defer func() { numCPU = origNumCPU }()

	cases := []struct {
		name     string
		override int
		jobsCap  int
		ncpu     int
		want     int
	}{
		{"override wins over large ncpu + cap", 8, 4, 16, 8},
		{"override wins over small ncpu", 1, 8, 16, 1},
		{"override wins regardless of cap", 12, 8, 16, 12},
		{"no override, configured cap 8, ncpu above cap", 0, 8, 16, 8},
		{"no override, configured cap 8, ncpu below cap returns ncpu", 0, 8, 4, 4},
		{"no override, configured cap 2 below ncpu", 0, 2, 16, 2},
		{"no override, cap 0 falls back to podmanJobsCapFallback", 0, 0, 16, podmanJobsCapFallback},
		{"no override, cap negative falls back", 0, -1, 16, podmanJobsCapFallback},
		{"no override, cap 8 but ncpu 1", 0, 8, 1, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			numCPU = func() int { return tc.ncpu }
			got := resolvePodmanJobs(tc.override, tc.jobsCap)
			if got != tc.want {
				t.Errorf("resolvePodmanJobs(%d, %d) with ncpu=%d = %d, want %d",
					tc.override, tc.jobsCap, tc.ncpu, got, tc.want)
			}
		})
	}
}
