package main

import (
	"slices"
	"strconv"
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

// jobsArg extracts the --jobs value from assembled build args, or "" if absent.
func jobsArg(args []string) string {
	for i, a := range args {
		if a == "--jobs" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// TestBuildLocalArgs_PodmanJobsCap verifies buildLocalArgs passes the capped
// value to podman (not raw NCPU). With no configured cap (podmanJobsCap == 0)
// the auto value falls back to podmanJobsCapFallback, so on a 16-core host the
// emitted --jobs is the fallback, not 16 (the historical crash trigger).
func TestBuildLocalArgs_PodmanJobsCap(t *testing.T) {
	origNumCPU := numCPU
	defer func() { numCPU = origNumCPU }()
	numCPU = func() int { return 16 } // simulate 16-core host

	cmd := &BuildCmd{Cache: "none"} // podmanJobsCap unset → fallback
	args := cmd.buildLocalArgs("podman", []string{"img:latest"}, "linux/amd64", "img", "ghcr.io/org")

	jobsVal := jobsArg(args)
	if jobsVal == "" {
		t.Fatal("--jobs not present in podman args")
	}
	want := strconv.Itoa(podmanJobsCapFallback)
	if jobsVal != want {
		t.Errorf("--jobs = %q, want %q (fallback cap on a host with NCPU > cap)", jobsVal, want)
	}
}

// TestBuildLocalArgs_ConfiguredCap verifies a configured cap (from
// defaults.podman_jobs_cap, resolved into BuildCmd.podmanJobsCap in Run()) is
// honored: on a 16-core host with cap 8, the emitted --jobs is 8.
func TestBuildLocalArgs_ConfiguredCap(t *testing.T) {
	origNumCPU := numCPU
	defer func() { numCPU = origNumCPU }()
	numCPU = func() int { return 16 }

	cmd := &BuildCmd{Cache: "none", podmanJobsCap: 8}
	args := cmd.buildLocalArgs("podman", []string{"img:latest"}, "linux/amd64", "img", "ghcr.io/org")

	if got := jobsArg(args); got != "8" {
		t.Errorf("--jobs = %q, want %q (configured cap honored)", got, "8")
	}
}

// TestBuildLocalArgs_PodmanJobsOverride verifies that an explicit
// --podman-jobs value wins over the default cap.
func TestBuildLocalArgs_PodmanJobsOverride(t *testing.T) {
	origNumCPU := numCPU
	defer func() { numCPU = origNumCPU }()
	numCPU = func() int { return 16 }

	cmd := &BuildCmd{
		Cache:      "none",
		PodmanJobs: 8,
	}
	args := cmd.buildLocalArgs("podman", []string{"img:latest"}, "linux/amd64", "img", "ghcr.io/org")

	var jobsVal string
	for i, a := range args {
		if a == "--jobs" && i+1 < len(args) {
			jobsVal = args[i+1]
			break
		}
	}
	if jobsVal != "8" {
		t.Errorf("--jobs = %q, want %q (override)", jobsVal, "8")
	}
}

// TestBuildLocalArgs_DockerEngineSkipsJobsFlag ensures the fix did not
// accidentally add --jobs to the docker code path (which never had it).
func TestBuildLocalArgs_DockerEngineSkipsJobsFlag(t *testing.T) {
	cmd := &BuildCmd{Cache: "none"}
	args := cmd.buildLocalArgs("docker", []string{"img:latest"}, "linux/amd64", "img", "ghcr.io/org")

	if slices.Contains(args, "--jobs") {
		t.Errorf("docker args should not include --jobs, got: %v", args)
		return
	}
}
