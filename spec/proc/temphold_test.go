package proc

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// temphold_test.go — the red/green pair for the sweep's liveness guard.
//
// RED on the pre-fix tree: a HELD, backdated temp was swept, because the two guards that existed
// (root mtime, /proc/<pid>/fd) both answer "stale" for a live build. GREEN: it survives, while a
// RELEASED backdated temp is still reaped — the property the sweep exists for must not regress.

// backdate pushes a path's mtime past the sweep's safety floor, standing in for the minutes a real
// build takes. It is the honest way to test an age-gated sweep: the alternative is sleeping for
// five minutes, which is the sleep-as-synchronization R4 forbids.
func backdate(t *testing.T, path string) {
	t.Helper()
	old := time.Now().Add(-2 * sweepSafetyFloor)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatalf("backdating %s: %v", path, err)
	}
}

// sweepableTestPrefix borrows a real swept namespace so the test exercises the production
// sweepablePatterns table rather than a fixture the sweep would ignore.
const sweepableTestPrefix = "charly-localpkg-"

// TestSweepStaleTemps_HeldTempSurvives is the RED arm: this is the build the roster lost.
func TestSweepStaleTemps_HeldTempSurvives(t *testing.T) {
	dir, release, err := MkdirTempHeld("", sweepableTestPrefix)
	if err != nil {
		t.Fatalf("MkdirTempHeld() error = %v", err)
	}
	defer release()
	defer func() { _ = os.RemoveAll(dir); UnregisterTempCleanup(dir) }()

	// Reproduce the exact shape that defeats both existing guards: the build writes into a
	// SUBDIRECTORY, which leaves the root's own mtime untouched, and nothing holds an open
	// descriptor on the root (a real build holds it as its CWD, invisible to /proc/<pid>/fd).
	if err := os.MkdirAll(filepath.Join(dir, "src", "charly"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "charly", "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	backdate(t, dir)

	SweepStaleTemps()

	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("SweepStaleTemps() deleted a HELD temp (%v) — this is the live-build deletion: a bed died here with its source tree gone mid-makepkg", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "src", "charly", "main.go")); err != nil {
		t.Errorf("the held temp's contents were removed (%v) — a partial RemoveAll is the same failure, just slower to notice", err)
	}
}

// TestSweepStaleTemps_UnheldCreatorIsSwept pins the DEFECT itself, in the shape every creator had
// before this fix: a bare os.MkdirTemp with nothing held. It must keep failing to survive — if this
// ever starts passing, the sweep stopped deleting live-looking trees for some unrelated reason and
// the guard above would be proving nothing.
//
// This is the decisive control for the fix as a whole. (Removing only the TempIsHeld probe from the
// sweep does NOT resurrect the defect, because MkdirTempHeld's open descriptor also satisfies the
// older /proc/<pid>/fd guard — see temphold.go on what each guard contributes.)
func TestSweepStaleTemps_UnheldCreatorIsSwept(t *testing.T) {
	dir, err := os.MkdirTemp("", sweepableTestPrefix)
	if err != nil {
		t.Fatal(err)
	}
	RegisterTempCleanup(dir)
	defer UnregisterTempCleanup(dir)
	if err := os.MkdirAll(filepath.Join(dir, "src", "charly"), 0o755); err != nil {
		t.Fatal(err)
	}
	backdate(t, dir)

	SweepStaleTemps()

	if _, err := os.Stat(dir); err == nil {
		_ = os.RemoveAll(dir)
		t.Fatal("the pre-fix creator shape survived the sweep — this control is no longer reproducing the deletion the fix prevents, so the held-temp test above proves nothing")
	}
}

// TestSweepStaleTemps_ReleasedTempIsSwept is the non-regression: the sweep must still do its job.
// A killed build releases its flock at process death, so this is what a real SIGKILL leaves behind.
func TestSweepStaleTemps_ReleasedTempIsSwept(t *testing.T) {
	dir, release, err := MkdirTempHeld("", sweepableTestPrefix)
	if err != nil {
		t.Fatalf("MkdirTempHeld() error = %v", err)
	}
	UnregisterTempCleanup(dir)
	release() // the holder is gone — exactly what the kernel does when a process dies
	backdate(t, dir)

	SweepStaleTemps()

	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		_ = os.RemoveAll(dir)
		t.Fatalf("SweepStaleTemps() left a RELEASED stale temp behind (stat err = %v) — the leak the sweep exists to prevent", err)
	}
}

// TestSweepStaleTemps_RecentTempSurvives keeps the pre-existing age guard honest: it is still a
// guard, not something the new probe replaced.
func TestSweepStaleTemps_RecentTempSurvives(t *testing.T) {
	dir, release, err := MkdirTempHeld("", sweepableTestPrefix)
	if err != nil {
		t.Fatalf("MkdirTempHeld() error = %v", err)
	}
	UnregisterTempCleanup(dir)
	release()
	defer func() { _ = os.RemoveAll(dir) }()

	SweepStaleTemps()

	if _, err := os.Stat(dir); err != nil {
		t.Errorf("SweepStaleTemps() removed a temp younger than the safety floor (%v)", err)
	}
}

// TestTempIsHeld_ReleasesOnProcessDeath proves the property the whole design rests on: the hold is
// kernel-owned, so a process killed with no chance to clean up still frees it. Without this, the
// fix would trade a deleted build for a permanent leak.
func TestTempIsHeld_ReleasesOnProcessDeath(t *testing.T) {
	dir := t.TempDir()
	if TempIsHeld(dir) {
		t.Fatal("TempIsHeld() = true for a fresh unheld dir")
	}

	// flock(1) is util-linux; skip rather than fail where it is absent.
	if _, err := exec.LookPath("flock"); err != nil {
		t.Skip("flock(1) not available")
	}
	// Own process GROUP, killed as a group below. flock(1) takes the lock and then EXECS the
	// command as a child that INHERITS the locked descriptor, so killing only the flock process
	// leaves the grandchild holding it — which is genuine kernel behavior, not a defect, and it
	// made the first cut of this test fail against correct code. Killing the group models what
	// actually happens when a build's whole process tree is OOM-killed.
	cmd := exec.Command("flock", "-x", dir, "sleep", "30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting holder: %v", err)
	}
	killGroup := func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	deadline := time.Now().Add(5 * time.Second)
	for !TempIsHeld(dir) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !TempIsHeld(dir) {
		killGroup()
		_ = cmd.Wait()
		t.Fatal("TempIsHeld() = false while another process holds the lock — the sweep would delete a live build")
	}

	// SIGKILL the whole group: no defer, no signal handler, no cleanup — the OOM case.
	killGroup()
	_ = cmd.Wait()
	for TempIsHeld(dir) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if TempIsHeld(dir) {
		t.Fatal("TempIsHeld() = true after the holder was SIGKILLed — the hold would leak and the sweep could never reap an abandoned temp again")
	}
}

// --- the $TMPDIR-vs-hardcoded-/tmp cutover ---

// altTempRoot returns an absolute temp root guaranteed NOT to live under /tmp, and registers its
// removal. Both tests below take their root from here, and neither may build it with t.TempDir().
//
// t.TempDir() and os.TempDir() sit on a PRECEDENCE CHAIN, not on independent variables.
// t.TempDir() roots at $GOTMPDIR when it is set; when it is not it falls back to os.TempDir(),
// which reads $TMPDIR, else /tmp. os.TempDir() reads only $TMPDIR, else /tmp. (Go 1.26
// testing.makeTempDir is os.MkdirTemp(os.Getenv("GOTMPDIR"), pattern), testing.go:1460.) The two
// can diverge only when GOTMPDIR is set, and then only if it names a different directory; with
// GOTMPDIR unset they always agree, because t.TempDir() DOES read TMPDIR, transitively, through
// that fallback.
//
// The governing condition for these tests is therefore not "is TMPDIR set" but "does t.TempDir()
// land outside /tmp". With BOTH variables unset — the stock CI default — it does not: the root
// is /tmp/Test…/001/, scaffolding inside the very directory the pre-fix hardcode looks at, which
// the pre-fix strings.HasPrefix(tgt, "/tmp/") accepts.
//
// Measured on the pre-fix body (bbb3c6a2), plain `go test`, all four combinations:
//
//	TMPDIR   GOTMPDIR   SweepStaleTemps_HonoursTMPDIR   OpenedFilesByAnyProcess_HonoursTMPDIR
//	set      set        FAIL                            FAIL
//	unset    set        FAIL                            FAIL
//	unset    unset      FAIL                            PASS   <- vacuous on a stock runner
//	set      unset      FAIL                            FAIL
//
// Only OpenedFilesByAnyProcess_HonoursTMPDIR was defective, and only in row three.
// SweepStaleTemps_HonoursTMPDIR discriminates in all four — but via the pre-fix glob being
// non-recursive, a property unrelated to what it asserts, which would go vacuous the day the
// sweep learned to recurse. Rooting both here makes both structural rather than incidental; the
// 2x2 above is FAIL in all eight cells with this helper in place, so nothing was weakened.
//
// The package directory is the root a Go test can rely on being outside /tmp: `go test` runs
// each test binary with its working directory set to the package's source directory. Symlinks
// are resolved because openedFilesByAnyProcess compares against /proc/<pid>/fd readlink targets,
// which the kernel reports fully resolved.
func altTempRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp(".", "spec-proc-tmproot-")
	if err != nil {
		t.Fatalf("creating an alternate temp root in the package dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("resolving %s: %v", dir, err)
	}
	if root, err = filepath.Abs(root); err != nil {
		t.Fatalf("absolutising %s: %v", dir, err)
	}
	if strings.HasPrefix(root, "/tmp/") {
		t.Fatalf("the alternate temp root %s is itself under /tmp — this checkout cannot host the premise of the $TMPDIR tests, which is a root the pre-fix hardcode does NOT cover; they would pass without discriminating", root)
	}
	return root
}

// TestSweepStaleTemps_HonoursTMPDIR is the gate for the hardcoded temp root in the sweeper. The
// two sweep tests above discriminate only when the ambient environment happens to export TMPDIR
// — red on a $TMPDIR host, green without it — so on a default runner they say nothing about this
// defect. This one sets TMPDIR itself, over a root altTempRoot() places outside /tmp. It fails on
// the pre-fix body in all four TMPDIR/GOTMPDIR combinations (see altTempRoot).
//
// The defect: every creator resolves through os.TempDir() (`proc.MkdirTempHeld("", …)`,
// `os.MkdirTemp("", …)`), which honours $TMPDIR, while the sweeper globbed a hardcoded "/tmp" —
// so on such a host it scanned a directory no temp had ever been created in and reaped nothing.
// Measured on one: 201 stale `charly-localpkg-` trees under $TMPDIR, zero under /tmp.
func TestSweepStaleTemps_HonoursTMPDIR(t *testing.T) {
	tmpdir := altTempRoot(t)
	t.Setenv("TMPDIR", tmpdir)

	// Created the way every real creator creates: an empty dir argument, resolved through
	// os.TempDir() — which now means tmpdir.
	dir, err := os.MkdirTemp("", sweepableTestPrefix)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(dir) != filepath.Clean(tmpdir) {
		t.Fatalf("os.MkdirTemp landed in %s, want a child of %s — the premise of this test is that creators honour $TMPDIR", dir, tmpdir)
	}
	RegisterTempCleanup(dir)
	defer UnregisterTempCleanup(dir)
	backdate(t, dir)

	SweepStaleTemps()

	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		_ = os.RemoveAll(dir)
		t.Fatalf("SweepStaleTemps() left %s behind (stat err = %v) — the sweep scanned a hardcoded /tmp while the creator honoured $TMPDIR, so on such a host it reaps nothing at all", dir, err)
	}
}

// TestOpenedFilesByAnyProcess_HonoursTMPDIR covers the more dangerous half of the same hardcode.
// openedFilesByAnyProcess is the sweep's guard (a) — "some process has this open" — and it
// filtered /proc/<pid>/fd targets by a hardcoded "/tmp/" prefix. On a $TMPDIR host it therefore
// recorded NOTHING, so every temp the sweep did reach looked unheld: the sweep does not merely
// fail to reap there, it loses a liveness guard.
//
// This process's own descriptor is visible in /proc/self/fd, so the assertion is deterministic.
// The root comes from altTempRoot() and must: built with t.TempDir() this test PASSED on the
// pre-fix body whenever BOTH TMPDIR and GOTMPDIR were unset — the stock CI default — so it was
// vacuous exactly where it mattered most. See altTempRoot's comment for the 2x2.
func TestOpenedFilesByAnyProcess_HonoursTMPDIR(t *testing.T) {
	tmpdir := altTempRoot(t)
	t.Setenv("TMPDIR", tmpdir)

	f, err := os.CreateTemp("", sweepableTestPrefix)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = f.Close()
		_ = os.Remove(f.Name())
	}()

	held := openedFilesByAnyProcess()
	if !held[f.Name()] {
		t.Fatalf("openedFilesByAnyProcess() did not record %s as held — the fd filter used a hardcoded /tmp prefix, so on a $TMPDIR host the sweep's liveness guard sees nothing and a LIVE temp reads as unheld", f.Name())
	}
}
