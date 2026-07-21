package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/opencharly/sdk/vmshared"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// blockingExecutor blocks RunCapture until the per-probe context is cancelled
// for any command containing blockOn (simulating a wedged `podman exec` under
// heavy load), and delegates everything else to the embedded fakeExecutor. It
// HONORS the passed ctx (the never-hang contract), so the only thing that can
// unblock it is the per-probe deadline this cutover introduced.
type blockingExecutor struct {
	*fakeExecutor
	blockOn string
}

func (b *blockingExecutor) RunInteractive(context.Context, string) (int, error) {
	return -1, spec.ErrNotSupported
}
func (b *blockingExecutor) RunStream(context.Context, string) (int, error) {
	return -1, spec.ErrNotSupported
}
func (b *blockingExecutor) RunCapture(ctx context.Context, cmd string) (string, string, int, error) {
	if b.blockOn != "" && strings.Contains(cmd, b.blockOn) {
		<-ctx.Done() // wedged: only the per-probe deadline frees us
		return "", "blocked", 0, ctx.Err()
	}
	return b.fakeExecutor.RunCapture(ctx, cmd)
}

// TestRunner_PerProbeNeverHang is the load-robustness regression guard: a single
// wedged probe must be cancelled INDIVIDUALLY (at ProbeTimeout) and the pass must
// continue to the next probe — instead of hanging the whole pass until the bed
// runner's outer timeout SIGKILLs the entire `charly check live` subprocess.
//
// Without the per-probe never-hang (the ctx-shadow in runOne), probe 1's
// RunCapture blocks on a never-cancelled context.Background() FOREVER, so r.Run
// never returns and the 5s watchdog below fails the test. With the fix, probe 1
// fails fast and probe 2 still runs and passes.
func TestRunner_PerProbeNeverHang(t *testing.T) {
	fake := &fakeExecutor{responses: []fakeResponse{
		{matchPrefix: "echo healthy", stdout: "ok\n"},
	}}
	be := &blockingExecutor{fakeExecutor: fake, blockOn: "WEDGEPROBE"}
	// a tight per-probe bound for the test
	r := newCheckRunner(kit.RunnerConfig{Exec: be, Mode: RunModeLive, Env: map[string]string{}, ProbeTimeout: 100 * time.Millisecond})

	checks := []spec.Op{
		{Plugin: "command", PluginInput: map[string]any{"command": "WEDGEPROBE check"}},                      // wedges → must be cancelled at ProbeTimeout
		{Plugin: "command", PluginInput: map[string]any{"command": "echo healthy"}, Stdout: matcherEq("ok")}, // must still run after the wedge
	}

	done := make(chan []kit.CheckResult, 1)
	go func() { done <- r.Run(context.Background(), checks) }()

	select {
	case results := <-done:
		if len(results) != 2 {
			t.Fatalf("want 2 results, got %d", len(results))
		}
		if results[0].Status != spec.StatusFail {
			t.Errorf("wedged probe: want spec.StatusFail (cancelled at per-probe deadline), got %v (%s)", results[0].Status, results[0].Message)
		}
		if results[1].Status != spec.StatusPass {
			t.Errorf("probe after the wedge: want spec.StatusPass (pass continued), got %v (%s)", results[1].Status, results[1].Message)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("r.Run hung on the wedged probe — per-probe never-hang not enforced (the whole-pass-guillotine regression)")
	}
}

// matcherEq builds a MatcherList asserting equality, mirroring the scalar YAML form.
func matcherEq(s string) spec.MatcherList { return spec.MatcherList{{Op: "equals", Value: s}} }

// TestRunner_ProbeNeverHang_HonorsAuthorTimeout: the per-probe ceiling is the
// floor (ProbeTimeout) unless the author declared a LONGER timeout:, which must
// be honored (+a small buffer) so a legitimately-slow probe is never cut short.
func TestRunner_ProbeNeverHang_HonorsAuthorTimeout(t *testing.T) {
	r := kit.NewRunner(kit.RunnerConfig{ProbeTimeout: 120 * time.Second})
	cases := []struct {
		name    string
		timeout string
		want    time.Duration
	}{
		{"no timeout → floor", "", 120 * time.Second},
		{"shorter timeout → floor", "10s", 120 * time.Second},
		{"longer timeout → honored + buffer", "5m", 5*time.Minute + 30*time.Second},
		{"unparseable → floor", "nonsense", 120 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := r.ProbeNeverHang(&spec.Op{Timeout: tc.timeout})
			if got != tc.want {
				t.Errorf("ProbeNeverHang(timeout=%q) = %s, want %s", tc.timeout, got, tc.want)
			}
		})
	}
	// A runner built the production way (newCheckRunner) gets the readiness per-attempt
	// floor as its per-probe never-hang, not the zero-value; that floor is
	// readinessPerAttemptFallback when no readiness config is loaded. (The bare
	// kit.NewRunner zero-value fallback is a kit-internal defensive const the host path
	// never hits, since newCheckRunner always sets ProbeTimeout from the readiness table.)
	if got := newCheckRunner(kit.RunnerConfig{}).ProbeNeverHang(&spec.Op{}); got != vmshared.ReadinessPerAttemptFallback {
		t.Errorf("newCheckRunner default: got %s, want readiness floor %s", got, vmshared.ReadinessPerAttemptFallback)
	}
}

// TestResolvedReadiness_PerAttemptHeavyForPollHeavy proves PollHeavy conds get
// the generous whole-pass per-attempt while every other class keeps the tight
// single-probe per-attempt — the poll.go half of the load-robustness fix.
func TestResolvedReadiness_PerAttemptHeavyForPollHeavy(t *testing.T) {
	var rr ResolvedReadiness // zero value → all fallback constants
	if rr.PerAttemptFor(vmshared.PollHeavy) != vmshared.ReadinessPerAttemptHeavyFallback {
		t.Fatalf("perAttemptHeavy fallback = %s, want %s", rr.PerAttemptFor(vmshared.PollHeavy), vmshared.ReadinessPerAttemptHeavyFallback)
	}
	for _, class := range []vmshared.PollClass{PollLocal, PollRemote} {
		if got := rr.WaitCapped("x", class, 0).PerAttempt; got != rr.PerAttemptFor(PollLocal) {
			t.Errorf("WaitCapped(class=%d).PerAttempt = %s, want single-probe %s", class, got, rr.PerAttemptFor(PollLocal))
		}
	}
	if got := rr.WaitCapped("x", vmshared.PollHeavy, 0).PerAttempt; got != rr.PerAttemptFor(vmshared.PollHeavy) {
		t.Errorf("WaitCapped(PollHeavy).PerAttempt = %s, want heavy %s", got, rr.PerAttemptFor(vmshared.PollHeavy))
	}
	if got := rr.Wait("x", vmshared.PollHeavy).PerAttempt; got != rr.PerAttemptFor(vmshared.PollHeavy) {
		t.Errorf("Wait(PollHeavy).PerAttempt = %s, want heavy %s", got, rr.PerAttemptFor(vmshared.PollHeavy))
	}
	// The heavy bound must be generously larger than the single-probe one — the
	// whole point is to stop the 120s mid-pass guillotine.
	if rr.PerAttemptFor(vmshared.PollHeavy) <= rr.PerAttemptFor(PollLocal) {
		t.Errorf("perAttemptHeavy (%s) must be > perAttempt (%s)", rr.PerAttemptFor(vmshared.PollHeavy), rr.PerAttemptFor(PollLocal))
	}
}

// TestReadinessConfig_PerAttemptHeavyOrdering guards the new ordering invariant:
// per_attempt <= per_attempt_heavy <= absolute_cap.
func TestReadinessConfig_PerAttemptHeavyOrdering(t *testing.T) {
	t.Run("valid passes", func(t *testing.T) {
		rc := &ReadinessConfig{PerAttempt: "120s", PerAttemptHeavy: "15m", AbsoluteCap: "30m"}
		if _, err := readinessResolve(rc); err != nil {
			t.Fatalf("valid config rejected: %v", err)
		}
	})
	t.Run("heavy < per_attempt rejected", func(t *testing.T) {
		rc := &ReadinessConfig{PerAttempt: "120s", PerAttemptHeavy: "60s"}
		if _, err := readinessResolve(rc); err == nil {
			t.Fatal("expected error: per_attempt_heavy < per_attempt")
		}
	})
	t.Run("heavy > absolute_cap rejected", func(t *testing.T) {
		rc := &ReadinessConfig{PerAttemptHeavy: "40m", AbsoluteCap: "30m"}
		if _, err := readinessResolve(rc); err == nil {
			t.Fatal("expected error: per_attempt_heavy > absolute_cap")
		}
	})
}
