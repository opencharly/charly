package main

import (
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
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
