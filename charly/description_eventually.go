package main

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// runWithEventually wraps a per-check verb handler in a retry loop when
// the check declares `eventually: <duration>`. The handler is called
// with no arguments — it closes over the check, runner, and context.
//
// Semantics:
//
//   - eventually:  outer retry cap (parsed as time.Duration).
//     Defaults the returned CheckResult to Attempts=1 when
//     unset (handler runs exactly once, unchanged).
//   - retry_interval: sleep between retries. Defaults to 1s.
//     Must be ≤ eventually or the loop would sleep past
//     the deadline on the first miss.
//   - PASS semantics: the FIRST attempt that returns TestPass wins;
//     its stdout/captures/message are what propagate.
//   - FAIL semantics: the LAST attempt before deadline is returned,
//     ensuring authors see the most recent failure
//     detail rather than a stale first-attempt error.
//   - SKIP semantics: treated the same as FAIL for retry purposes —
//     skips aren't actionable to retry against.
//
// Variable expansion: the handler re-runs from the same expanded check
// each attempt. The caller must expand variables BEFORE calling
// runWithEventually — re-expansion per attempt would re-evaluate
// ${CAPTURED:name} mid-run, which is unwanted (captures record on
// PASS only, and pre-pass attempts wouldn't see their own future
// capture).
//
// Context: runWithEventually honours ctx.Deadline / ctx.Done — a
// canceled context short-circuits the loop with the last attempt's
// result.
// killedProbeRetries bounds automatic re-attempts of a probe KILLED before it could
// produce a result (a per-attempt-deadline SIGKILL under a transient host-load spike,
// OOM). Rides out a compile-burst spike that starves ONE probe; a SUSTAINED-saturation
// host exhausts the retries and fails loudly (never an infinite retry). NOT retry-on-flake
// (R4): only an infra INTERRUPTION (never-completed probe) is re-run — a probe that RAN and
// returned a real exit is authoritative and never retried here.
const killedProbeRetries = 3

// killedProbeRetryInterval is a var (not const) so tests can shrink it; production value
// rides out a transient compile-burst spike (a few seconds) without a long tail.
var killedProbeRetryInterval = 3 * time.Second

// probeWasKilled reports whether a CheckResult reflects a probe terminated by a signal
// before completing (vs a probe that ran and failed). Keyed on the shared marker
// runCaptureCmd stamps into the signal-kill error (deploy_executor.go, R3).
func probeWasKilled(r CheckResult) bool {
	// A probe killed by hitting its OWN per-attempt deadline (DeadlineExceeded, set in
	// runOne's dispatch closure) is an authoritative "too slow" failure, NOT an infra
	// interruption — re-running it just re-hangs to the same deadline. Only an EXTERNAL
	// signal-kill (host OOM/load, ctx not deadline-exceeded) is a retryable interruption.
	return r.Status == TestFail && strings.Contains(r.Message, signalKillErrMarker) && !r.DeadlineExceeded
}

func runWithEventually(ctx context.Context, check *Op, handler func() CheckResult) CheckResult {
	if check == nil || check.Eventually == "" {
		result := handler()
		// A probe KILLED before completing produced NO real result — re-attempt it a
		// bounded number of times so a transient load spike that starves one probe does
		// not spuriously fail the check (the 2026-07 check-box "exit -1" under load). A
		// probe that RAN and failed is NOT retried here (that is the author's `eventually:`).
		for attempt := 1; attempt <= killedProbeRetries && probeWasKilled(result); attempt++ {
			select {
			case <-ctx.Done():
				return result
			case <-time.After(killedProbeRetryInterval):
			}
			result = handler()
			result.Attempts = attempt + 1
		}
		if result.Attempts == 0 {
			result.Attempts = 1
		}
		if result.TotalElapsed == 0 {
			result.TotalElapsed = result.Elapsed
		}
		return result
	}

	deadlineD, err := time.ParseDuration(check.Eventually)
	if err != nil {
		r := CheckResult{
			Op:      check,
			Status:  TestFail,
			Message: fmt.Sprintf("invalid eventually duration %q: %v", check.Eventually, err),
		}
		r.Attempts = 0
		return r
	}
	interval := time.Second
	if check.RetryInterval != "" {
		if d, perr := time.ParseDuration(check.RetryInterval); perr == nil {
			interval = d
		} else {
			r := CheckResult{
				Op:      check,
				Status:  TestFail,
				Message: fmt.Sprintf("invalid retry_interval %q: %v", check.RetryInterval, perr),
			}
			return r
		}
	}
	if interval > deadlineD {
		// Author error — defensive clamp so the loop at least runs once.
		interval = deadlineD
	}

	start := time.Now()
	deadline := start.Add(deadlineD)

	var last CheckResult
	attempts := 0
	for {
		attempts++
		last = handler()
		last.Attempts = attempts
		last.TotalElapsed = time.Since(start)
		if last.Status == TestPass {
			return last
		}
		if time.Now().Add(interval).After(deadline) {
			// Next sleep would cross the deadline — return the last
			// attempt's result as the final outcome.
			last.Status = TestFail
			if last.Message == "" {
				last.Message = fmt.Sprintf("did not pass within %s (%d attempt%s)",
					deadlineD, attempts, plural(attempts))
			} else {
				last.Message = fmt.Sprintf("%s (after %d attempt%s over %s)",
					last.Message, attempts, plural(attempts), last.TotalElapsed.Round(time.Millisecond))
			}
			return last
		}
		select {
		case <-ctx.Done():
			last.Status = TestFail
			last.Message = fmt.Sprintf("context canceled during eventually retry: %v", ctx.Err())
			return last
		case <-time.After(interval):
		}
	}
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
