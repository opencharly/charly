package main

// poll_probe_neverhang_test.go — TestRunner_PerProbeNeverHang + blockingExecutor and
// TestRunner_ProbeNeverHang_HonorsAuthorTimeout relocated to sdk/kit's own test suite (#55
// decoupling cone, Batch D — sdk/kit/runner_test.go): the load-robustness per-probe never-hang
// regression and the ProbeNeverHang floor-vs-author-timeout calculation both assert
// kit.Runner's OWN behavior, with zero real charly dispatch coupling once driven via
// RunnerConfig{ProbeTimeout: ...} directly instead of a charly-constructed kit.Runner. The
// readiness-floor-wiring assertion (newCheckRunner's construction sets ProbeTimeout from the
// readiness table) moved to candy/plugin-check/plugin_runner_floor_test.go, since that
// construction itself moved plugin-side in the #55 CHECK-ENGINE cone.

import (
	"testing"

	"github.com/opencharly/spec/poll"
	"github.com/opencharly/spec/spec"
)

// TestResolvedReadiness_PerAttemptHeavyForPollHeavy proves PollHeavy conds get
// the generous whole-pass per-attempt while every other class keeps the tight
// single-probe per-attempt — the poll.go half of the load-robustness fix.
func TestResolvedReadiness_PerAttemptHeavyForPollHeavy(t *testing.T) {
	var rr poll.ResolvedReadiness // zero value → all fallback constants
	if rr.PerAttemptFor(poll.PollHeavy) != poll.ReadinessPerAttemptHeavyFallback {
		t.Fatalf("perAttemptHeavy fallback = %s, want %s", rr.PerAttemptFor(poll.PollHeavy), poll.ReadinessPerAttemptHeavyFallback)
	}
	for _, class := range []poll.PollClass{poll.PollLocal, poll.PollRemote} {
		if got := rr.WaitCapped("x", class, 0).PerAttempt; got != rr.PerAttemptFor(poll.PollLocal) {
			t.Errorf("WaitCapped(class=%d).PerAttempt = %s, want single-probe %s", class, got, rr.PerAttemptFor(poll.PollLocal))
		}
	}
	if got := rr.WaitCapped("x", poll.PollHeavy, 0).PerAttempt; got != rr.PerAttemptFor(poll.PollHeavy) {
		t.Errorf("WaitCapped(PollHeavy).PerAttempt = %s, want heavy %s", got, rr.PerAttemptFor(poll.PollHeavy))
	}
	if got := rr.Wait("x", poll.PollHeavy).PerAttempt; got != rr.PerAttemptFor(poll.PollHeavy) {
		t.Errorf("Wait(PollHeavy).PerAttempt = %s, want heavy %s", got, rr.PerAttemptFor(poll.PollHeavy))
	}
	// The heavy bound must be generously larger than the single-probe one — the
	// whole point is to stop the 120s mid-pass guillotine.
	if rr.PerAttemptFor(poll.PollHeavy) <= rr.PerAttemptFor(poll.PollLocal) {
		t.Errorf("perAttemptHeavy (%s) must be > perAttempt (%s)", rr.PerAttemptFor(poll.PollHeavy), rr.PerAttemptFor(poll.PollLocal))
	}
}

// TestReadinessConfig_PerAttemptHeavyOrdering guards the new ordering invariant:
// per_attempt <= per_attempt_heavy <= absolute_cap.
func TestReadinessConfig_PerAttemptHeavyOrdering(t *testing.T) {
	t.Run("valid passes", func(t *testing.T) {
		rc := &spec.ReadinessConfig{PerAttempt: "120s", PerAttemptHeavy: "15m", AbsoluteCap: "30m"}
		if _, err := poll.ResolveReadiness(rc); err != nil {
			t.Fatalf("valid config rejected: %v", err)
		}
	})
	t.Run("heavy < per_attempt rejected", func(t *testing.T) {
		rc := &spec.ReadinessConfig{PerAttempt: "120s", PerAttemptHeavy: "60s"}
		if _, err := poll.ResolveReadiness(rc); err == nil {
			t.Fatal("expected error: per_attempt_heavy < per_attempt")
		}
	})
	t.Run("heavy > absolute_cap rejected", func(t *testing.T) {
		rc := &spec.ReadinessConfig{PerAttemptHeavy: "40m", AbsoluteCap: "30m"}
		if _, err := poll.ResolveReadiness(rc); err == nil {
			t.Fatal("expected error: per_attempt_heavy > absolute_cap")
		}
	})
}
