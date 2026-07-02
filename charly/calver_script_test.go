package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestCalverScriptDeterministic locks the build-time version-stamp invariant:
// pkg/arch/calver.sh derives the CalVer ONLY from the HEAD commit date, so EVERY
// binary built from one commit reports the IDENTICAL `charly version` — a dirty
// working-tree `task build:charly`, the clean git+file:// makepkg clone, an AUR
// build. The single source of truth (charly_calver) is shared by taskfiles/Build.yml
// and the PKGBUILD's pkgver()+build(); this test guards the bash side that the Go
// CharlyVersion()/ComputeCalVerAt path (version_test.go) cannot reach.
//
// It FAILS against the prior wall-clock fallback: that stamped `date -u` for a
// dirty tree, so a dirty build disagreed with a clean clone of the same commit
// (the pacman-pkgver vs `charly version` split) and a stale binary could falsely sort
// "newer". The dirty-tree assertion below reproduces exactly that case.
func TestCalverScriptDeterministic(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	script, err := filepath.Abs(filepath.Join("..", "pkg", "arch", "calver.sh"))
	if err != nil {
		t.Fatalf("resolving calver.sh path: %v", err)
	}
	if _, err := os.Stat(script); err != nil {
		t.Skipf("calver.sh not present (submodule not checked out?): %v", err)
	}

	dir := t.TempDir()
	// A FIXED commit timestamp → a deterministic expected CalVer, independent of
	// when the test runs. 2026-01-02 03:04:05 UTC → year 2026, day-of-year 2,
	// HHMM 3*100+4 = 304 → "2026.002.0304" (HHMM 4-digit zero-padded, matching charly_calver).
	const fixedDate = "2026-01-02T03:04:05 +0000"
	const want = "2026.002.0304"

	runGit := func(env []string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), env...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	runGit(nil, "init", "-q")
	runGit(nil, "config", "user.email", "t@example.com")
	runGit(nil, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(nil, "add", "a.txt")
	runGit([]string{"GIT_AUTHOR_DATE=" + fixedDate, "GIT_COMMITTER_DATE=" + fixedDate},
		"commit", "-q", "-m", "fixed")

	calver := func() string {
		t.Helper()
		cmd := exec.Command("bash", script)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("calver.sh: %v\n%s", err, out)
		}
		return strings.TrimSpace(string(out))
	}

	// Clean tree → commit date.
	if got := calver(); got != want {
		t.Fatalf("clean tree: charly_calver = %q, want %q", got, want)
	}
	// Dirty the tree by MODIFYING A TRACKED file — an unstaged tracked change,
	// exactly the shape of a dev `task build:charly` over edited charly/*.go that the old
	// wall-clock branch detected (`git diff --quiet` → false) and stamped with the
	// clock. The deterministic rule keeps the HEAD commit date. (An *untracked*
	// file would NOT trip the old `git diff`, so it is not a valid guard input.)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("modified\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := calver(); got != want {
		t.Fatalf("dirty (modified-tracked) tree: charly_calver = %q, want %q — a dirty build must NOT wall-clock (the regression this guards)", got, want)
	}
	// Stage the modification — `git diff --cached --quiet` → false; still HEAD date.
	runGit(nil, "add", "a.txt")
	if got := calver(); got != want {
		t.Fatalf("staged tree: charly_calver = %q, want %q", got, want)
	}
}
