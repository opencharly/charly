package main

import (
	"os/exec"
	"reflect"
	"sort"
	"testing"
	"time"
)

// TestHarnessPhaseRe_BoundaryNotProgress verifies the boundary regex
// matches the per-phase boundary line emitted at the start of each
// phase but does NOT match progress / watchdog lines that share the
// `phase N/M` token. Triggering a resync on every progress line would
// hammer credentials at the 5-minute cadence — the boundary marker is
// the only correct trigger.
func TestHarnessPhaseRe_BoundaryNotProgress(t *testing.T) {
	cases := []struct {
		line string
		want string // captured phase number; "" if no match expected
	}{
		// boundary lines — must match
		{"harness: phase 1/8 — recipes [single-pod-system-state] (10 scenarios)", "1"},
		{"harness: phase 7/8 — recipes [a b c d e f g] (74 scenarios)", "7"},
		{"harness: phase 12/12 — recipes [r] (1 scenarios)", "12"},
		// progress / watchdog / unrelated lines — must NOT match
		{"harness: progress [phase 4/8 iter 1] +5m0s into the run — current score 21/53 (no improvement observed yet)", ""},
		{"harness: watchdog [phase 4/8 iter 1] terminating AI runner — no scoring progress for 35m0s", ""},
		{"harness: score=default ai=claude exit=plateau iterations=8 best=53/61", ""},
		{"score live: pod \"redis\" unreachable: ...", ""},
		{"", ""},
	}
	for _, tc := range cases {
		m := checkPhaseRe.FindStringSubmatch(tc.line)
		var got string
		if len(m) >= 2 {
			got = m[1]
		}
		if got != tc.want {
			t.Errorf("line=%q want=%q got=%q", tc.line, tc.want, got)
		}
	}
}

// TestRunWithPhaseResync_BoundariesTriggerResyncSkippingPhase1 drives
// runWithPhaseResync with a fake subprocess that emits five
// phase-boundary stderr lines (phases 1..5). The mock phaseResyncFn
// records each call; the assertion is that phases 2,3,4,5 each trigger
// exactly one resync and phase 1 triggers none — preflight has already
// covered phase 1, and re-syncing on it would be wasted work right at
// the moment claude is about to read its credentials for iter 1.
func TestRunWithPhaseResync_BoundariesTriggerResyncSkippingPhase1(t *testing.T) {
	callCh := make(chan int, 16)
	var seenScore string

	origFn := phaseResyncFn
	phaseResyncFn = func(scoreName string, phase int) error {
		seenScore = scoreName
		callCh <- phase
		return nil
	}
	defer func() { phaseResyncFn = origFn }()

	// Fake subprocess emits boundary markers for phases 1..5 plus one
	// progress line that should NOT trigger a resync.
	cmd := exec.Command("sh", "-c", `
		echo "harness: phase 1/8 — recipes [r1] (1 scenarios)" >&2
		echo "harness: phase 2/8 — recipes [r1 r2] (2 scenarios)" >&2
		echo "harness: progress [phase 2/8 iter 1] +5m0s into the run — current score 1/2" >&2
		echo "harness: phase 3/8 — recipes [r1 r2 r3] (3 scenarios)" >&2
		echo "harness: phase 4/8 — recipes [r1 r2 r3 r4] (4 scenarios)" >&2
		echo "harness: phase 5/8 — recipes [r1 r2 r3 r4 r5] (5 scenarios)" >&2
	`)
	if err := runWithPhaseResync(cmd, "test-score"); err != nil {
		t.Fatalf("runWithPhaseResync returned error: %v", err)
	}

	// Drain the expected 4 resyncs (phases 2, 3, 4, 5). Generous timeout
	// to absorb goroutine scheduling jitter on busy CI hosts.
	var got []int
	timeout := time.After(3 * time.Second)
	for len(got) < 4 {
		select {
		case p := <-callCh:
			got = append(got, p)
		case <-timeout:
			t.Fatalf("only received %d resyncs in 3s; got=%v (expected 4 — for phases 2,3,4,5)", len(got), got)
		}
	}

	// Verify no extra resync arrives within a short grace window (e.g.
	// from the progress line, which would be a regression).
	select {
	case extra := <-callCh:
		t.Errorf("unexpected extra resync for phase %d", extra)
	case <-time.After(150 * time.Millisecond):
	}

	sort.Ints(got)
	want := []int{2, 3, 4, 5}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("phase resync set: got=%v want=%v", got, want)
	}
	if seenScore != "test-score" {
		t.Errorf("scoreName forwarded to phaseResyncFn: got=%q want=%q", seenScore, "test-score")
	}
}

// TestRunWithPhaseResync_ResyncFailureDoesNotAbortRun verifies that a
// resync error is logged but does NOT propagate to the caller — a
// transient sync failure (network blip, etc.) must not kill the
// in-pod orchestrator's run.
func TestRunWithPhaseResync_ResyncFailureDoesNotAbortRun(t *testing.T) {
	origFn := phaseResyncFn
	phaseResyncFn = func(scoreName string, phase int) error {
		return &simulatedResyncError{phase: phase}
	}
	defer func() { phaseResyncFn = origFn }()

	cmd := exec.Command("sh", "-c", `
		echo "harness: phase 2/3 — recipes [a b] (2 scenarios)" >&2
		echo "stage 1 done" >&2
	`)
	// Expect: cmd.Wait() returns nil because sh exits 0 and the
	// resync's error is swallowed (logged) inside the goroutine.
	if err := runWithPhaseResync(cmd, "test-score"); err != nil {
		t.Fatalf("runWithPhaseResync should swallow resync errors: %v", err)
	}
	// Brief sleep so the goroutine's stderr log lands before the test
	// returns (no assertion on stderr; we just want to keep the
	// goroutine from leaking past the test).
	time.Sleep(100 * time.Millisecond)
}

type simulatedResyncError struct{ phase int }

func (e *simulatedResyncError) Error() string { return "simulated resync failure" }
