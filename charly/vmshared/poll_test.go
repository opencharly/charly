package vmshared

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

// fakeClock drives pollUntil deterministically: after(d) advances the clock by
// d (modelling the inter-tick sleep) and fires immediately, so tests never sleep
// for real. PerAttempt is set huge in these tests so the real-time per-attempt
// context never fires (it is exercised separately in TestPollUntil_PerAttempt).
type fakeClock struct{ t time.Time }

func (f *fakeClock) now() time.Time { return f.t }
func (f *fakeClock) after(d time.Duration) <-chan time.Time {
	f.t = f.t.Add(d)
	ch := make(chan time.Time, 1)
	ch <- f.t
	close(ch)
	return ch
}

func fakeCfg(c *fakeClock, name string) PollConfig {
	return PollConfig{Name: name, Interval: 15 * time.Second, PerAttempt: time.Hour, now: c.now, after: c.after}
}

// 1. A monotonically advancing high-water mark is waited for indefinitely (never
// stalls) and returns nil when ready.
func TestPollUntil_AdvancingNeverStalls(t *testing.T) {
	c := &fakeClock{t: time.Unix(0, 0)}
	cfg := fakeCfg(c, "advancing")
	cfg.NoProgress = 90 * time.Second
	cfg.AbsoluteCap = 30 * time.Minute
	var n float64
	err := pollUntil(context.Background(), cfg, func(context.Context) (bool, float64, error) {
		n++
		return n >= 50, n, nil // climbs 1,2,...; ready at 50 (>> NoProgress/interval ticks)
	})
	if err != nil {
		t.Fatalf("advancing marker must be waited for, got %v", err)
	}
}

// 2. A frozen high-water mark stalls at NoProgress.
func TestPollUntil_FrozenStalls(t *testing.T) {
	c := &fakeClock{t: time.Unix(0, 0)}
	cfg := fakeCfg(c, "frozen")
	cfg.NoProgress = 90 * time.Second // 6 ticks @ 15s
	cfg.AbsoluteCap = 30 * time.Minute
	err := pollUntil(context.Background(), cfg, func(context.Context) (bool, float64, error) {
		return false, 5, nil // frozen at 5
	})
	if !errors.Is(err, ErrPollStalled) {
		t.Fatalf("frozen marker must stall, got %v", err)
	}
}

// 3. An oscillating-BELOW-peak marker (crash loop) is detected as a stall — NOT
// masked as progress (the high-water must-fix).
func TestPollUntil_OscillatingCrashLoopStalls(t *testing.T) {
	c := &fakeClock{t: time.Unix(0, 0)}
	cfg := fakeCfg(c, "crashloop")
	cfg.NoProgress = 90 * time.Second
	cfg.AbsoluteCap = 30 * time.Minute
	vals := []float64{5, 3, 5, 2, 5, 3, 5, 4, 5, 1} // never exceeds the first peak (5)
	i := 0
	err := pollUntil(context.Background(), cfg, func(context.Context) (bool, float64, error) {
		v := vals[i%len(vals)]
		i++
		return false, v, nil
	})
	if !errors.Is(err, ErrPollStalled) {
		t.Fatalf("a crash loop oscillating below its peak must stall, got %v", err)
	}
}

// 4. Cap-only mode (NoProgress=0) waits until the absolute cap regardless of marker.
func TestPollUntil_CapOnlyExceeds(t *testing.T) {
	c := &fakeClock{t: time.Unix(0, 0)}
	cfg := fakeCfg(c, "cap")
	cfg.NoProgress = 0 // binary/edge mode
	cfg.AbsoluteCap = 90 * time.Second
	err := pollUntil(context.Background(), cfg, func(context.Context) (bool, float64, error) {
		return false, 0, nil // never ready, no progress signal
	})
	if !errors.Is(err, ErrPollCapExceeded) {
		t.Fatalf("cap-only never-ready must hit the cap, got %v", err)
	}
}

// 5. Cap-only waits out a frozen marker and SUCCEEDS when readiness finally flips
// (the binary/edge case — proves no-progress would have wrongly failed it).
func TestPollUntil_CapOnlyBinaryFlipSucceeds(t *testing.T) {
	c := &fakeClock{t: time.Unix(0, 0)}
	cfg := fakeCfg(c, "binary")
	cfg.NoProgress = 0
	cfg.AbsoluteCap = 30 * time.Minute
	ticks := 0
	err := pollUntil(context.Background(), cfg, func(context.Context) (bool, float64, error) {
		ticks++
		return ticks >= 40, 0, nil // frozen "down" for 40 ticks (~10m), then up
	})
	if err != nil {
		t.Fatalf("a binary marker that flips before the cap must succeed, got %v", err)
	}
}

// 6. ErrPollFatal aborts immediately (does not wait out the window).
func TestPollUntil_FatalAbortsNow(t *testing.T) {
	c := &fakeClock{t: time.Unix(0, 0)}
	cfg := fakeCfg(c, "fatal")
	cfg.NoProgress = 90 * time.Second
	cfg.AbsoluteCap = 30 * time.Minute
	calls := 0
	err := pollUntil(context.Background(), cfg, func(context.Context) (bool, float64, error) {
		calls++
		return false, 0, fmt.Errorf("ssh process died: %w", ErrPollFatal)
	})
	if !errors.Is(err, ErrPollFatal) {
		t.Fatalf("want fatal, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("fatal must abort after ONE cond call, got %d", calls)
	}
}

// 7. Transient errors do not advance the high-water mark → still stalls.
func TestPollUntil_TransientErrorsDoNotAdvance(t *testing.T) {
	c := &fakeClock{t: time.Unix(0, 0)}
	cfg := fakeCfg(c, "transient")
	cfg.NoProgress = 90 * time.Second
	cfg.AbsoluteCap = 30 * time.Minute
	var seen atomic.Int32
	cfg.OnTransient = func(error) { seen.Add(1) }
	err := pollUntil(context.Background(), cfg, func(context.Context) (bool, float64, error) {
		return false, 0, errors.New("connection refused") // transient forever
	})
	if !errors.Is(err, ErrPollStalled) {
		t.Fatalf("transient-forever must stall, got %v", err)
	}
	if seen.Load() == 0 {
		t.Fatal("OnTransient should have fired")
	}
}

// 8. A cond that BLOCKS in its data phase is cancelled per attempt — the poll
// terminates instead of hanging forever (the never-hang must-fix). Real clock,
// small durations.
func TestPollUntil_PerAttemptBoundsBlockingCond(t *testing.T) {
	cfg := PollConfig{Name: "blocking", Interval: 5 * time.Millisecond, PerAttempt: 20 * time.Millisecond, AbsoluteCap: 200 * time.Millisecond}
	done := make(chan error, 1)
	go func() {
		done <- pollUntil(context.Background(), cfg, func(ctx context.Context) (bool, float64, error) {
			select {
			case <-ctx.Done(): // honors the per-attempt timeout
				return false, 0, ctx.Err()
			case <-time.After(time.Hour): // would block ~forever otherwise
				return true, 0, nil
			}
		})
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrPollCapExceeded) {
			t.Fatalf("blocking cond should be per-attempt-cancelled then hit the cap, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pollUntil HUNG on a blocking cond — per-attempt bound failed")
	}
}

// 9. Context cancellation wins.
func TestPollUntil_ContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cfg := PollConfig{Name: "cancel", Interval: time.Second, PerAttempt: time.Second, AbsoluteCap: time.Hour}
	err := pollUntil(ctx, cfg, func(c context.Context) (bool, float64, error) {
		return false, 0, c.Err()
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

// 10. ready=true returns nil immediately.
func TestPollUntil_ReadyImmediately(t *testing.T) {
	cfg := PollConfig{Name: "ready", Interval: time.Second, PerAttempt: time.Second, AbsoluteCap: time.Hour}
	if err := pollUntil(context.Background(), cfg, func(context.Context) (bool, float64, error) { return true, 0, nil }); err != nil {
		t.Fatalf("ready must return nil, got %v", err)
	}
}

// 11. Self-validation fails closed.
func TestPollUntil_ValidationFailsClosed(t *testing.T) {
	cond := func(context.Context) (bool, float64, error) { return false, 0, nil }
	cases := []PollConfig{
		{Name: "no-interval", Interval: 0, PerAttempt: time.Second, AbsoluteCap: time.Minute},
		{Name: "no-per-attempt", Interval: time.Second, PerAttempt: 0, AbsoluteCap: time.Minute},
		{Name: "no-bounds", Interval: time.Second, PerAttempt: time.Second, NoProgress: 0, AbsoluteCap: 0},
	}
	for _, cfg := range cases {
		if err := pollUntil(context.Background(), cfg, cond); !errors.Is(err, ErrPollConfig) {
			t.Fatalf("%s: want ErrPollConfig, got %v", cfg.Name, err)
		}
	}
}

// 12. The builders produce sane, never-hang configs (zero-value RR → fallbacks).
func TestResolvedReadiness_Builders(t *testing.T) {
	var rr ResolvedReadiness // zero value → fallback constants
	w := rr.Wait("w", PollHeavy)
	if w.NoProgress != readinessNoProgressFallback || w.AbsoluteCap != readinessAbsoluteCapFallback || w.Interval != readinessIntervalHeavyFallback {
		t.Fatalf("Wait zero-value fallbacks wrong: %+v", w)
	}
	wc := rr.WaitCapped("wc", PollRemote, 7*time.Minute)
	if wc.NoProgress != 0 || wc.AbsoluteCap != 7*time.Minute {
		t.Fatalf("WaitCapped should be cap-only at the given cap: %+v", wc)
	}
	wp := rr.WatchProgress("wp", PollHeavy, 0)
	if wp.AbsoluteCap != 0 || wp.NoProgress != readinessNoProgressFallback {
		t.Fatalf("WatchProgress should be no-cap monotonic: %+v", wp)
	}
	sg := rr.StopGate("sg")
	if sg.NoProgress != 0 || sg.AbsoluteCap != readinessStopGraceFallback {
		t.Fatalf("StopGate should be cap-only at StopGrace: %+v", sg)
	}
	for _, cfg := range []PollConfig{w, wc, wp, sg} {
		if err := cfg.validate(); err != nil {
			t.Fatalf("builder %q produced invalid config: %v", cfg.Name, err)
		}
	}
}
