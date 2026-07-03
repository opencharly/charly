package main

import (
	"context"
	"testing"
	"time"
)

// A probe KILLED before completing (signal, never produced a result) is re-attempted a
// bounded number of times in the single-shot (no `eventually:`) path — so a transient
// host-load spike that starves ONE probe does not spuriously fail the check. A probe that
// RAN and failed is NOT retried; a probe killed on EVERY attempt fails loudly after the cap.
func TestRunWithEventually_RetriesKilledProbe(t *testing.T) {
	orig := killedProbeRetryInterval
	killedProbeRetryInterval = time.Millisecond // don't wall-clock-block the test
	defer func() { killedProbeRetryInterval = orig }()

	killed := func() CheckResult {
		return CheckResult{Status: TestFail, Message: "probe failed: process " + signalKillErrMarker + " (signal: killed)"}
	}
	pass := func() CheckResult { return CheckResult{Status: TestPass, Message: "ok"} }
	ranAndFailed := func() CheckResult { return CheckResult{Status: TestFail, Message: "exists=false, want true"} }

	t.Run("killed-then-pass retries and passes", func(t *testing.T) {
		calls := 0
		h := func() CheckResult {
			calls++
			if calls == 1 {
				return killed() // transient spike kills the first attempt
			}
			return pass() // spike passed → retry succeeds
		}
		got := runWithEventuallyNoSleep(t, h)
		if got.Status != TestPass {
			t.Fatalf("killed-then-pass: status = %v, want Pass (retry must re-run a killed probe)", got.Status)
		}
		if calls != 2 {
			t.Errorf("expected 2 attempts (1 killed + 1 retry pass), got %d", calls)
		}
	})

	t.Run("ran-and-failed is NOT retried", func(t *testing.T) {
		calls := 0
		h := func() CheckResult { calls++; return ranAndFailed() }
		got := runWithEventuallyNoSleep(t, h)
		if got.Status != TestFail {
			t.Fatalf("ran-and-failed: status = %v, want Fail", got.Status)
		}
		if calls != 1 {
			t.Errorf("a probe that RAN and failed must NOT be retried (that would be retry-on-flake), got %d attempts", calls)
		}
	})

	t.Run("deadline-killed is NOT retried (re-running re-hangs to the same deadline)", func(t *testing.T) {
		calls := 0
		h := func() CheckResult {
			calls++
			// The group-kill in runCaptureCmd surfaces a per-attempt-deadline SIGKILL as a
			// signal-kill, but DeadlineExceeded marks it as the probe's OWN deadline.
			return CheckResult{Status: TestFail, Message: "probe " + signalKillErrMarker + " (signal: killed)", DeadlineExceeded: true}
		}
		got := runWithEventuallyNoSleep(t, h)
		if got.Status != TestFail {
			t.Fatalf("deadline-killed: status = %v, want Fail", got.Status)
		}
		if calls != 1 {
			t.Errorf("a probe killed by its OWN per-attempt deadline must NOT be retried (that re-hangs), got %d attempts", calls)
		}
	})

	t.Run("killed on every attempt fails loudly after the cap", func(t *testing.T) {
		calls := 0
		h := func() CheckResult { calls++; return killed() }
		got := runWithEventuallyNoSleep(t, h)
		if got.Status != TestFail {
			t.Fatalf("always-killed: status = %v, want Fail (must fail loudly, never infinite-retry)", got.Status)
		}
		if calls != killedProbeRetries+1 {
			t.Errorf("always-killed: got %d attempts, want %d (1 + %d bounded retries)", calls, killedProbeRetries+1, killedProbeRetries)
		}
	})
}

// runWithEventuallyNoSleep drives runWithEventually with an already-cancelled-on-interval
// context so the bounded retry loop does not actually sleep in the test. The retry loop's
// select fires on time.After(killedProbeRetryInterval) OR ctx.Done(); we want it to take
// the time.After branch (retry) but not wall-clock-block, so we pass a background ctx and
// accept the real (short) sleeps — killedProbeRetries is tiny, so the test stays fast.
func runWithEventuallyNoSleep(t *testing.T, h func() CheckResult) CheckResult {
	t.Helper()
	// No `eventually:` → single-shot path (the one carrying the infra-kill retry).
	return runWithEventually(context.Background(), &Op{}, h)
}

