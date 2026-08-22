package lock

import (
	"os"
	"path/filepath"
	"testing"
)

// The first tests in this package. They are CAPABILITY-CREATION coverage, not regression
// coverage: there is no before/after to compare, because the behaviour they pin was never
// asserted anywhere. A reviewer looking for a failing-then-passing pair will not find one.
//
// They gate the two halves of one change — the line deleted and the line deliberately kept,
// which fail in opposite directions so no single test sees both — plus the CONTRACT the
// deletion depends on (a caller writing after acquiring keeps its bytes) and the fact that
// the flock is taken at all, without which the content assertions are vacuous.

// TestAcquireFileLock_WritesNothing pins the DELETED half: a lock file carries no content of
// its own. It previously carried "pid=%d", which nothing read and which taught every human
// who opened it that this is a pidfile lock whose staleness must be reasoned about. It is
// not — the kernel releases an flock when the holder dies, so the file's presence proves
// nothing and its absence proves nothing.
//
// Fails if the pid write (or any other decorative content) is reintroduced.
func TestAcquireFileLock_WritesNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.lock")
	release, err := AcquireFileLock(path, true)
	if err != nil {
		t.Fatalf("AcquireFileLock: %v", err)
	}
	defer func() { _ = release() }()

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read lock file: %v", err)
	}
	if len(b) != 0 {
		t.Fatalf("lock file carries %d bytes (%q); it must be empty — content in a lock file is read by nothing and implies a mechanism this lock does not have", len(b), string(b))
	}
}

// TestAcquireFileLock_TruncatesStaleContent pins the KEPT half: the Truncate(0). It reads as
// dead code now that nothing is written after it, so it is one tidy-up from deletion — and
// deleting it is worse than the defect that was removed.
//
// os.OpenFile(O_CREATE|O_RDWR) does NOT truncate. Without the Truncate, a re-acquired lock
// retains the PREVIOUS holder's bytes, turning an empty file into a stale one. Staleness
// reads as currency, so that is a worse artifact than the pid line ever was.
//
// Fails if Truncate(0) is removed.
func TestAcquireFileLock_TruncatesStaleContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stale.lock")

	// A previous holder that wrote content — the candy/plugin-box pattern.
	release, err := AcquireFileLock(path, true)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if err := os.WriteFile(path, []byte("2026.001.0000\n"), 0o644); err != nil {
		t.Fatalf("write content: %v", err)
	}
	if err := release(); err != nil {
		t.Fatalf("first release: %v", err)
	}

	// The next holder must find an empty file, not the previous holder's CalVer.
	release2, err := AcquireFileLock(path, true)
	if err != nil {
		t.Fatalf("second acquire: %v", err)
	}
	defer func() { _ = release2() }()

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read lock file: %v", err)
	}
	if len(b) != 0 {
		t.Fatalf("re-acquired lock still holds the previous holder's %d bytes (%q) — O_CREATE|O_RDWR does not truncate, so removing Truncate(0) leaves stale content that reads as current", len(b), string(b))
	}
}

// TestAcquireFileLock_WriteAfterAcquireSurvives is NOT a third assertion in a list — it is
// the LICENCE for the deletion, and it is the case where all the risk lives.
//
// Removing the pid write is safe only because the one caller that needs the file's content
// writes its own AFTER acquiring: candy/plugin-box records its build CalVer there, and
// candy/plugin-clean's retention floor reads it back. Every other caller wanted nothing.
//
// So the fix removes something twelve callers did not need, and the entire risk sits with the
// ONE that did — which is exactly where the coverage has to be strongest. Until this test
// existed, that safety rested on two people having read box.go:373-377. Now the PACKAGE
// guarantees writers are unaffected, so the deletion survives a fourteenth call site
// appearing with nobody re-reading anything.
func TestAcquireFileLock_WriteAfterAcquireSurvives(t *testing.T) {
	path := filepath.Join(t.TempDir(), "build-activity.lock")
	const want = "2026.227.2300\n"

	release, err := AcquireFileLock(path, true)
	if err != nil {
		t.Fatalf("AcquireFileLock: %v", err)
	}
	defer func() { _ = release() }()

	if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
		t.Fatalf("write caller content: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(b) != want {
		t.Fatalf("caller content = %q, want %q — a caller that writes AFTER acquiring must keep what it wrote", string(b), want)
	}
}

// TestAcquireFileLock_NonBlockingBusy pins the behaviour every caller's error path depends
// on, and which the two tests above would silently pass without: a second NON-blocking
// acquire of a held lock reports ErrLockBusy rather than succeeding. Without this, a fix
// that broke locking entirely — never actually taking the flock — would leave both content
// assertions green.
func TestAcquireFileLock_NonBlockingBusy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "busy.lock")

	release, err := AcquireFileLock(path, true)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer func() { _ = release() }()

	if _, err := AcquireFileLock(path, false); err == nil {
		t.Fatal("second non-blocking acquire succeeded on a held lock — the flock is not being taken")
	}
}
