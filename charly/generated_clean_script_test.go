package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// generatedCleanScript resolves scripts/check-generated-clean.sh — the guard that keeps
// pluginsgen + the workspace `go build` from leaving a tracked file dirty. It is resolved
// by PATH and a missing script FAILS the test, rather than t.Skip: a guard whose coverage
// skips when the guard is absent proves nothing, and this test is the only coverage the
// guard has (scripts/bootstrap-charly.sh itself needs a full build to reach it).
func generatedCleanScript(t *testing.T) string {
	t.Helper()
	script, err := filepath.Abs(filepath.Join("..", "scripts", "check-generated-clean.sh"))
	if err != nil {
		t.Fatalf("resolving check-generated-clean.sh path: %v", err)
	}
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("scripts/check-generated-clean.sh is missing (%v) — it is the guard that keeps a build from leaving the tracked generated files dirty, and this test cannot pass without it", err)
	}
	return script
}

// generatedCleanFiles is the fixture's committed set. It is deliberately NOT read from the
// script's --list: the test asserts --list EQUALS this, so silently dropping a path from
// the guard (weakening it) fails here instead of passing unnoticed.
var generatedCleanFiles = []string{
	"go.work",
	"go.work.sum",
	"charly/plugins_generated.go",
	"charly/plugins_refs_generated.go",
}

// generatedCleanFixture builds a temp git repo holding those four files, committed and
// clean — the state a checkout is in before a build.
func generatedCleanFixture(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	runGit("init", "-q")
	runGit("config", "user.email", "t@example.com")
	runGit("config", "user.name", "Test")
	for _, rel := range generatedCleanFiles {
		writeGeneratedCleanFile(t, dir, rel, committedBody(rel))
	}
	runGit("add", ".")
	runGit("commit", "-q", "-m", "fixture")
	return dir
}

func committedBody(rel string) string { return "# committed " + rel + "\n" }

func writeGeneratedCleanFile(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readGeneratedCleanFile(t *testing.T, dir, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

type generatedCleanRun struct {
	stdout string
	stderr string
	code   int
}

func runGeneratedCleanScript(t *testing.T, dir string, args ...string) generatedCleanRun {
	t.Helper()
	cmd := exec.Command("bash", append([]string{generatedCleanScript(t)}, args...)...)
	cmd.Dir = dir
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("running check-generated-clean.sh: %v", err)
		}
		code = exitErr.ExitCode()
	}
	return generatedCleanRun{stdout: stdout.String(), stderr: stderr.String(), code: code}
}

// TestGeneratedCleanScriptClassifiesEveryRewrittenFile pins the guard's canonical list and
// the two classes it splits into. The classes are the whole point: a file DERIVED FROM
// TRACKED INPUTS must fail here (this checkout can be fixed), while the upstream-derived
// index must not (nobody here can fix it, and in a detached submodule the remedy is
// forbidden) — see TestPluginsRefsIndexSound for the well-defined gate that replaces
// byte-equality for that file.
func TestGeneratedCleanScriptClassifiesEveryRewrittenFile(t *testing.T) {
	dir := generatedCleanFixture(t)
	got := runGeneratedCleanScript(t, dir, "--list")
	if got.code != 0 {
		t.Fatalf("--list exit = %d, want 0\nstderr: %s", got.code, got.stderr)
	}
	want := []string{
		"inconsistent go.work",
		"inconsistent go.work.sum",
		"inconsistent charly/plugins_generated.go",
		"upstream charly/plugins_refs_generated.go",
	}
	if gotList := strings.TrimRight(got.stdout, "\n"); gotList != strings.Join(want, "\n") {
		t.Fatalf("--list =\n%s\nwant\n%s", gotList, strings.Join(want, "\n"))
	}

	// A clean tree passes silently — no notice, no diff, nothing for a reader to triage.
	clean := runGeneratedCleanScript(t, dir)
	if clean.code != 0 {
		t.Fatalf("clean tree: exit = %d, want 0\nstderr: %s", clean.code, clean.stderr)
	}
	if clean.stderr != "" || clean.stdout != "" {
		t.Fatalf("clean tree printed output — a passing guard must stay silent\nstdout: %q\nstderr: %q", clean.stdout, clean.stderr)
	}
}

// TestGeneratedCleanScriptFailsOnInconsistentDrift: a file derived from TRACKED inputs is
// stale, so the checkout contradicts itself. It FAILS, names the path, shows the diff — and
// leaves the file ALONE, because the remedy is a human decision to review and commit it.
func TestGeneratedCleanScriptFailsOnInconsistentDrift(t *testing.T) {
	dir := generatedCleanFixture(t)
	for _, rel := range []string{"go.work", "go.work.sum", "charly/plugins_generated.go"} {
		writeGeneratedCleanFile(t, dir, rel, "# drift "+rel+"\n")
		got := runGeneratedCleanScript(t, dir)
		if got.code != 1 {
			t.Fatalf("%s drifted: exit = %d, want 1\nstderr: %s", rel, got.code, got.stderr)
		}
		if !strings.Contains(got.stderr, rel) {
			t.Errorf("%s drifted: stderr does not name it\n%s", rel, got.stderr)
		}
		if !strings.Contains(got.stderr, "STALE") {
			t.Errorf("%s drifted: stderr does not report the class\n%s", rel, got.stderr)
		}
		if body := readGeneratedCleanFile(t, dir, rel); body != "# drift "+rel+"\n" {
			t.Errorf("%s drifted: the guard rewrote it (%q) — an inconsistent file must be left for review, never restored", rel, body)
		}
		// Restore the fixture for the next iteration.
		writeGeneratedCleanFile(t, dir, rel, committedBody(rel))
	}
}

// TestGeneratedCleanScriptRestoresUpstreamDriftAndSucceeds is the opencharly/charly#792
// regression: pluginsgen's index is a projection of the corpus's DEFAULT BRANCHES, so an
// upstream plugin adding a provider makes the tracked index stale on a build that succeeded
// for every reason under this repository's control. The guard must NAME it, RESTORE the
// tracked file so the tree stays clean at its gitlink, and still exit 0 — the build's
// artifact is not in question.
func TestGeneratedCleanScriptRestoresUpstreamDriftAndSucceeds(t *testing.T) {
	dir := generatedCleanFixture(t)
	const rel = "charly/plugins_refs_generated.go"
	writeGeneratedCleanFile(t, dir, rel, "# drift from an upstream plugin\n")

	got := runGeneratedCleanScript(t, dir)
	if got.code != 0 {
		t.Fatalf("upstream drift: exit = %d, want 0 (an upstream change is not this checkout's defect)\nstderr: %s", got.code, got.stderr)
	}
	if !strings.Contains(got.stderr, "NOTICE") || !strings.Contains(got.stderr, rel) {
		t.Errorf("upstream drift: stderr must NOTICE the file by name\n%s", got.stderr)
	}
	if body := readGeneratedCleanFile(t, dir, rel); body != committedBody(rel) {
		t.Fatalf("upstream drift: %s = %q, want it restored to the committed content (%q)", rel, body, committedBody(rel))
	}
}

// TestGeneratedCleanScriptHandlesBothClassesInOneRun: the classes are independent, so one
// run must not stop at the first. An upstream restore must still happen on a run that FAILS
// for an inconsistent file — otherwise the tree keeps a drift nobody can act on, and the
// next run re-reports it forever.
func TestGeneratedCleanScriptHandlesBothClassesInOneRun(t *testing.T) {
	dir := generatedCleanFixture(t)
	const upstream = "charly/plugins_refs_generated.go"
	writeGeneratedCleanFile(t, dir, "go.work", "# drift\n")
	writeGeneratedCleanFile(t, dir, upstream, "# drift from an upstream plugin\n")

	got := runGeneratedCleanScript(t, dir)
	if got.code != 1 {
		t.Fatalf("both classes drifted: exit = %d, want 1 (the inconsistent file must still fail the run)\nstderr: %s", got.code, got.stderr)
	}
	if body := readGeneratedCleanFile(t, dir, upstream); body != committedBody(upstream) {
		t.Errorf("both classes drifted: %s = %q, want it restored even though the run failed", upstream, body)
	}
	if body := readGeneratedCleanFile(t, dir, "go.work"); body != "# drift\n" {
		t.Errorf("both classes drifted: go.work = %q, want the inconsistent file left for review", body)
	}
}

// TestGeneratedCleanScriptReportsNoLineageOutsideGit: from a source export with no .git
// there is nothing to compare a working tree against, so the guard says so with its own exit
// code instead of silently reporting "clean" (a silent pass is the failure mode this whole
// file exists to prevent).
func TestGeneratedCleanScriptReportsNoLineageOutsideGit(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("bash", generatedCleanScript(t))
	cmd.Dir = dir
	// A t.TempDir() under a checked-out repository would otherwise find that repository's
	// .git by walking up, making this case vacuous.
	cmd.Env = append(os.Environ(), "GIT_CEILING_DIRECTORIES="+dir)
	out, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("outside a git worktree: err = %v (want a non-zero exit)\n%s", err, out)
	}
	if exitErr.ExitCode() != 2 {
		t.Fatalf("outside a git worktree: exit = %d, want 2\n%s", exitErr.ExitCode(), out)
	}
	if !strings.Contains(string(out), "not inside a git worktree") {
		t.Errorf("outside a git worktree: output does not explain the exit code\n%s", out)
	}
}
