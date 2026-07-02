package sdk

import (
	"sync/atomic"
	"testing"
	"time"
)

// TestParentWatch_FiresOnParentDeath proves the orphan backstop self-terminates
// the plugin once the parent PID changes (the parent died → reparented). Drives
// runParentWatch with injected fakes so no real fork is needed.
func TestParentWatch_FiresOnParentDeath(t *testing.T) {
	const startPPID = 4242
	fired := make(chan struct{}, 1)
	// getppid reports the parent is GONE from the first poll (reparented to 1).
	getppid := func() int { return 1 }
	go runParentWatch(startPPID, time.Millisecond, getppid, func() { fired <- struct{}{} })

	select {
	case <-fired:
		// reaped — correct
	case <-time.After(2 * time.Second):
		t.Fatal("runParentWatch did not fire onOrphaned after the parent PID changed")
	}
}

// TestParentWatch_QuietWhileParentLives proves the watch does NOT fire while the
// parent is alive (getppid stable) — the property that keeps the unbounded
// credential await-unlock RPC undisturbed against a LIVE host. It then lets the
// parent "die" so the watch goroutine exits cleanly (no leak).
func TestParentWatch_QuietWhileParentLives(t *testing.T) {
	const startPPID = 4242
	var ppid atomic.Int64
	ppid.Store(startPPID)
	var fires atomic.Int64
	go runParentWatch(startPPID, time.Millisecond, func() int { return int(ppid.Load()) }, func() { fires.Add(1) })

	// Many poll intervals must pass with the parent alive and ZERO fires.
	time.Sleep(50 * time.Millisecond)
	if got := fires.Load(); got != 0 {
		t.Fatalf("watch fired %d times while the parent was alive; want 0", got)
	}

	// Now the parent dies — the watch must fire exactly once and the goroutine exit.
	ppid.Store(1)
	deadline := time.After(2 * time.Second)
	for fires.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("watch did not fire after the parent died")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	// Give the (now-returned) goroutine a moment; it must not fire a second time.
	time.Sleep(20 * time.Millisecond)
	if got := fires.Load(); got != 1 {
		t.Fatalf("watch fired %d times; want exactly 1 (fires once then returns)", got)
	}
}
