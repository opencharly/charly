package main

import (
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/opencharly/sdk/kit"
)

// A non-blocking acquire of an already-held lock must report errLockBusy, and
// the lock must be re-acquirable once released — the duplicate-run guard.
func TestAcquireFileLock_NonBlockingBusyThenReusable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")

	rel1, err := acquireFileLock(path, false)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if _, err := acquireFileLock(path, false); !errors.Is(err, errLockBusy) {
		t.Fatalf("second acquire while held: want errLockBusy, got %v", err)
	}
	if err := rel1(); err != nil {
		t.Fatalf("release: %v", err)
	}
	rel2, err := acquireFileLock(path, false)
	if err != nil {
		t.Fatalf("re-acquire after release: %v", err)
	}
	if err := rel2(); err != nil {
		t.Fatalf("release after re-acquire: %v", err)
	}
}

// A blocking acquire must WAIT for the current holder to release rather than
// failing — and it must not proceed before the release. Deterministic without a
// sleep: the child blocks in flock and can only proceed after rel1() runs, by
// which point `released` is already set.
func TestAcquireFileLock_BlockingWaitsForRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "y.lock")

	rel1, err := acquireFileLock(path, true)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	var released atomic.Bool
	got := make(chan error, 1)
	go func() {
		rel2, err := acquireFileLock(path, true) // blocks until rel1() runs
		if err != nil {
			got <- err
			return
		}
		defer func() { _ = rel2() }()
		if !released.Load() {
			got <- errors.New("blocking acquire returned before the holder released")
			return
		}
		got <- nil
	}()

	released.Store(true)
	if err := rel1(); err != nil {
		t.Fatalf("release: %v", err)
	}
	if err := <-got; err != nil {
		t.Fatal(err)
	}
}

// TestAcquireBuildActivityLock_WritesContentAndReleases covers the write side of the
// live-build-floor mechanism now split across core (this file: acquireBuildActivityLock
// WRITES a flocked nonce file under kit.BuildActivityDir) and candy/plugin-clean's
// retention engine (the READ side: liveBuildFloor scans that same dir — see
// candy/plugin-clean/retention_floor_test.go for its coverage). Proves the held lock's
// file carries the CalVer content, is still flock-held while acquired, and both the
// lock and its content disappear on release.
func TestAcquireBuildActivityLock_WritesContentAndReleases(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	dir := filepath.Join(cache, "charly", "locks", "builds")

	rel, err := acquireBuildActivityLock("2026.188.1900")
	if err != nil {
		t.Fatal(err)
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read build-activity dir: %v", err)
	}
	if len(ents) != 1 {
		t.Fatalf("want 1 lock file, got %d: %v", len(ents), ents)
	}
	lockPath := filepath.Join(dir, ents[0].Name())
	content, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(content); got != "2026.188.1900\n" {
		t.Fatalf("lock content = %q, want %q", got, "2026.188.1900\n")
	}
	// Still held — a non-blocking acquire of the SAME path must report busy.
	if _, err := kit.AcquireFileLock(lockPath, false); !errors.Is(err, kit.ErrLockBusy) {
		t.Fatalf("expected the lock to still be held, got %v", err)
	}

	if err := rel(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatal("lock file must be removed on release")
	}
}
