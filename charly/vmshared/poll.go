package vmshared

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// poll.go — the ONE load-robust readiness-poll primitive (R3 + R4).
//
// Before this, charly had ~14 hand-rolled poll/wait/timeout loops (stepReady,
// waitForContainerReady, WaitForSSH, WaitForCloudInit, execReboot, waitStopped,
// gracefulStopDomain, the libvirt-XML shutoff loop, runWithEventually,
// GuestAgent.ExecAndWait, ProgressWatchdog, …), each with its OWN fixed
// wall-clock deadline (6m / 5m / 120s / 30s / 60s / 10s …). That is an R3
// duplication AND an R4 magic-number problem: under heavy PARALLEL testing,
// concurrent builds starve service startup, so a slow-but-still-progressing
// deploy blows past those short fixed deadlines and fails — even though it would
// have become ready. pollUntil replaces all of them with one synchronization
// primitive whose bounds are config-sourced + validated (see ReadinessConfig).
//
// Two honest readiness modes — chosen by the call-site builders below, because
// a single "no-progress" rule is WRONG for half the sites:
//
//   - MONOTONIC sites (an observable count climbs as startup advances: AI score,
//     supervisord children settled, check-live pass tally). These use the
//     NO-PROGRESS WATCHDOG: the deadline resets whenever progress reaches a NEW
//     HIGH-WATER mark, so a slow-but-advancing startup is waited for and only a
//     genuine stall (high-water frozen for NoProgress) fails fast. High-water,
//     not any-change — a crash loop that oscillates BELOW its peak never resets,
//     so it is detected, not masked (this is exactly ProgressWatchdog.bestScore).
//
//   - BINARY / EDGE sites (the marker is frozen for the whole legitimate wait
//     and only flips at the very end: sshd refused→up, boot_id down→up,
//     cloud-init running→done, a file appearing, a channel binding). For these a
//     no-progress window is NOT a stall detector — it degrades to a plain
//     timeout. They use CAP-ONLY mode (NoProgress=0) with a GENEROUS,
//     config-sourced AbsoluteCap that replaces the old too-short fixed deadline.
//     The generous cap is what makes heavy parallel testing work; per-attempt
//     bounding (below) is what keeps it from hanging.
//
// Never-hang is guaranteed by THREE independent mechanisms, not just the cap:
//   1. PerAttempt: every cond() call runs under context.WithTimeout, so a cond
//      that BLOCKS in its data phase (a wedged `podman exec`, a black-holed ssh
//      session) is cancelled per tick — the bounds are checked BETWEEN ticks, so
//      a cond that never returns would otherwise defeat AbsoluteCap entirely.
//      Conds MUST honor the passed ctx (exec.CommandContext, ssh keepalives).
//      PerAttempt is sized for ONE atomic probe; a PollHeavy cond (each tick is a
//      whole multi-probe `charly check live` pass) is NOT one atomic probe, so it
//      uses the much larger PerAttemptHeavy instead — the inner pass bounds its
//      OWN probes individually (Runner.probeNeverHang), so the heavy per-attempt
//      is just a generous "this pass is taking unreasonably long" backstop, NOT a
//      mid-pass guillotine that kills a slow-but-progressing pass under heavy load.
//   2. AbsoluteCap: a generous wall-clock ceiling (or NoProgress when monotonic).
//   3. ctx cancellation: a dispatcher-level cancel wins at the inter-tick select.
// pollUntil self-validates these at entry (fail-closed) — it does not trust the
// config-load validation, because direct/zero-value construction bypasses it.

// PollCondition is the caller's readiness probe, invoked once per tick under a
// per-attempt timeout context.
//
//	ready    — the awaited state is reached; pollUntil returns nil.
//	progress — a MONOTONIC forward-motion score (higher == more progress, e.g. a
//	           count or an AI score). The no-progress watchdog resets only when
//	           this reaches a NEW HIGH-WATER value, so oscillation/regression
//	           cannot masquerade as progress. Ignored in cap-only mode
//	           (NoProgress<=0) — pass 0 for binary/edge sites.
//	err      — wrap ErrPollFatal to abort IMMEDIATELY (unrecoverable: process
//	           died, auth/permission error). Any other non-nil err is transient
//	           (target not up yet): routed to OnTransient, treated as not-ready,
//	           and does NOT advance the high-water mark.
type PollCondition func(ctx context.Context) (ready bool, progress float64, err error)

var (
	// ErrPollStalled — the high-water mark did not advance within NoProgress.
	ErrPollStalled = errors.New("poll: no forward progress within the no-progress window")
	// ErrPollCapExceeded — the absolute wall-clock ceiling elapsed.
	ErrPollCapExceeded = errors.New("poll: absolute cap exceeded")
	// ErrPollFatal — wrap a cond error in this to abort the poll immediately.
	ErrPollFatal = errors.New("poll: fatal condition error")
	// ErrPollConfig — pollUntil's own entry validation rejected the config.
	ErrPollConfig = errors.New("poll: invalid configuration")
)

// PollConfig parameterizes one pollUntil call. Build it via ResolvedReadiness's
// Wait / WaitCapped / StopGate so no call site carries a magic literal.
type PollConfig struct {
	Name        string        // label woven into log lines + errors, e.g. "ssh-ready check-k3s-vm"
	Interval    time.Duration // tick cadence (pacing only; never the deadline). >0 required.
	PerAttempt  time.Duration // per-cond context timeout (the never-hang kill-switch for a blocking probe). >0 required.
	NoProgress  time.Duration // monotonic stall window; 0 disables (cap-only mode).
	AbsoluteCap time.Duration // generous never-hang ceiling; 0 means "no cap" (NoProgress must then be >0).

	// OnTick (optional) is host-side observability per tick. NEVER AI-visible
	// (matches check_watchdog.go doctrine). sinceAdvance is time since the last
	// high-water advance (0 in cap-only mode).
	OnTick func(elapsed time.Duration, progress float64, sinceAdvance time.Duration)
	// OnTransient (optional) receives each transient (non-fatal) cond error.
	OnTransient func(err error)

	now   func() time.Time                     // injected clock; nil → time.Now (test seam)
	after func(time.Duration) <-chan time.Time // injected timer; nil → time.After (test seam)
}

func (c PollConfig) clock() func() time.Time {
	if c.now != nil {
		return c.now
	}
	return time.Now
}

func (c PollConfig) timer() func(time.Duration) <-chan time.Time {
	if c.after != nil {
		return c.after
	}
	return time.After
}

// validate fails closed: a misconfigured poll must error, never spin or hang.
func (c PollConfig) validate() error {
	if c.Interval <= 0 {
		return fmt.Errorf("%w: %s Interval must be > 0 (else the tick select busy-spins)", ErrPollConfig, c.Name)
	}
	if c.PerAttempt <= 0 {
		return fmt.Errorf("%w: %s PerAttempt must be > 0 (the per-cond never-hang bound)", ErrPollConfig, c.Name)
	}
	if c.NoProgress <= 0 && c.AbsoluteCap <= 0 {
		return fmt.Errorf("%w: %s needs at least one active bound (NoProgress>0 or AbsoluteCap>0) — else a never-ready cond loops forever", ErrPollConfig, c.Name)
	}
	return nil
}

// pollUntil drives cond to readiness using the modes described on PollConfig.
// Returns nil on ready; a %w-wrapped ErrPollStalled / ErrPollCapExceeded /
// ErrPollFatal / ErrPollConfig, or ctx.Err(), otherwise. Context-cancellable;
// deterministic under an injected clock (zero real sleeping in tests).
func pollUntil(ctx context.Context, cfg PollConfig, cond PollCondition) error {
	if err := cfg.validate(); err != nil {
		return err
	}
	now := cfg.clock()
	after := cfg.timer()

	start := now()
	const noBaseline = -1 // sentinel: no high-water recorded yet (mirrors ProgressWatchdog bestScore=-1)
	best := float64(noBaseline)
	haveBaseline := false
	var lastAdvance time.Time // zero until the first real advance

	for {
		// Per-attempt bound: a cond that blocks in its data phase is cancelled
		// here, so the bounds below are actually reachable.
		attemptCtx, cancel := context.WithTimeout(ctx, cfg.PerAttempt)
		ready, progress, err := cond(attemptCtx)
		cancel()

		switch {
		case ready:
			return nil
		case err != nil && errors.Is(err, ErrPollFatal):
			return fmt.Errorf("%s: %w", cfg.Name, err)
		case err != nil:
			// Transient: not up yet. Not progress, not regress — like the
			// watchdog's probe-error skip. Do NOT advance the high-water mark.
			if cfg.OnTransient != nil {
				cfg.OnTransient(err)
			}
		default:
			// err == nil && !ready: a clean progress observation.
			if cfg.NoProgress > 0 {
				if !haveBaseline {
					// Baseline-first: record but do NOT count as an advance
					// (else an opening score trivially resets the timer — the
					// exact ProgressWatchdog baseline rule).
					best = progress
					haveBaseline = true
				} else if progress > best {
					best = progress
					lastAdvance = now()
				}
			}
		}

		elapsed := now().Sub(start)
		if cfg.OnTick != nil {
			var sinceAdvance time.Duration
			if cfg.NoProgress > 0 && !lastAdvance.IsZero() {
				sinceAdvance = now().Sub(lastAdvance)
			}
			cfg.OnTick(elapsed, progress, sinceAdvance)
		}

		// Bound checks (between ticks).
		if cfg.NoProgress > 0 {
			// idle = time since the last high-water advance; before any advance,
			// measure from start (avoids a trivial-baseline race — matches the
			// watchdog's lastImprovedAt.IsZero() → elapsed rule).
			idle := elapsed
			if !lastAdvance.IsZero() {
				idle = now().Sub(lastAdvance)
			}
			if idle >= cfg.NoProgress {
				return fmt.Errorf("%s: stalled (no forward progress for %s, high-water %g): %w",
					cfg.Name, cfg.NoProgress, best, ErrPollStalled)
			}
		}
		if cfg.AbsoluteCap > 0 && elapsed >= cfg.AbsoluteCap {
			return fmt.Errorf("%s: %w (cap %s)", cfg.Name, ErrPollCapExceeded, cfg.AbsoluteCap)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-after(cfg.Interval):
		}
	}
}

// PollClass selects the tick-interval band only (never the deadline). Local =
// in-process/loopback probes (podman exec, libvirt domain-state, supervisord);
// Remote = one-ssh/network-round-trip probes; Heavy = full-suite subprocess
// probes (each tick runs a whole `charly check live` pass).
type PollClass int

const (
	PollLocal PollClass = iota
	PollRemote
	PollHeavy
)

// ResolvedReadiness is the load-time-materialized, validated bounds set
// (resolveReadiness in build.go merges flag/env → defaults.readiness → the named
// fallback constants, then ReadinessConfig.Validate enforces ordering). The
// zero value is SAFE — every builder falls back to the compiled-in fallback
// constants — so a site that has not yet been threaded the resolved set still
// gets sane, never-hang bounds (it just isn't operator-tunable until threaded).
type ResolvedReadiness struct {
	IntervalLocal   time.Duration
	IntervalRemote  time.Duration
	IntervalHeavy   time.Duration
	PerAttempt      time.Duration
	PerAttemptHeavy time.Duration
	NoProgress      time.Duration
	AbsoluteCap     time.Duration
	StopGrace       time.Duration
}

// Named fallback constants — the single source of the defaults, referenced by
// both resolveReadiness (config.go) and the zero-value path here. Not magic
// literals scattered at call sites: one named home, config-overridable, validated.
const (
	readinessIntervalLocalFallback  = 250 * time.Millisecond
	readinessIntervalRemoteFallback = 3 * time.Second
	readinessIntervalHeavyFallback  = 15 * time.Second
	readinessPerAttemptFallback     = 120 * time.Second
	// PollHeavy per-attempt: ONE tick is a whole `charly check live` pass (100+
	// probes), so the per-attempt that bounds it must accommodate the FULL pass
	// under heavy parallel load — NOT the single-probe 120s. The inner pass
	// bounds each probe at PerAttempt individually (Runner.probeNeverHang), so
	// this is the generous never-hang backstop, not a mid-pass guillotine.
	readinessPerAttemptHeavyFallback = 15 * time.Minute
	readinessNoProgressFallback      = 90 * time.Second
	readinessAbsoluteCapFallback     = 30 * time.Minute
	readinessStopGraceFallback       = 180 * time.Second
)

func (rr ResolvedReadiness) interval(class PollClass) time.Duration {
	pick := func(v, fb time.Duration) time.Duration {
		if v > 0 {
			return v
		}
		return fb
	}
	switch class {
	case PollLocal:
		return pick(rr.IntervalLocal, readinessIntervalLocalFallback)
	case PollHeavy:
		return pick(rr.IntervalHeavy, readinessIntervalHeavyFallback)
	default:
		return pick(rr.IntervalRemote, readinessIntervalRemoteFallback)
	}
}

func (rr ResolvedReadiness) perAttempt() time.Duration {
	if rr.PerAttempt > 0 {
		return rr.PerAttempt
	}
	return readinessPerAttemptFallback
}

// perAttemptHeavy is the per-attempt bound for a PollHeavy cond (a whole
// multi-probe `charly check live` pass). Generous on purpose — see the
// readinessPerAttemptHeavyFallback comment.
func (rr ResolvedReadiness) perAttemptHeavy() time.Duration {
	if rr.PerAttemptHeavy > 0 {
		return rr.PerAttemptHeavy
	}
	return readinessPerAttemptHeavyFallback
}

// perAttemptFor picks the right per-attempt bound for the poll class: PollHeavy
// (a whole-pass cond) gets the generous heavy bound; everything else (single
// atomic probes) gets the standard per-attempt never-hang.
func (rr ResolvedReadiness) perAttemptFor(class PollClass) time.Duration {
	if class == PollHeavy {
		return rr.perAttemptHeavy()
	}
	return rr.perAttempt()
}

// Wait builds a MONOTONIC readiness PollConfig: the no-progress watchdog is
// active (the load-robust early-out), with a generous absolute cap as the
// never-hang backstop. Use ONLY where cond reports a genuinely advancing
// high-water progress score (supervisord settled-count, check-live pass tally,
// AI score). For binary/edge markers use WaitCapped instead.
func (rr ResolvedReadiness) Wait(name string, class PollClass) PollConfig {
	noProg := rr.NoProgress
	if noProg <= 0 {
		noProg = readinessNoProgressFallback
	}
	cap := rr.AbsoluteCap
	if cap <= 0 {
		cap = readinessAbsoluteCapFallback
	}
	return PollConfig{Name: name, Interval: rr.interval(class), PerAttempt: rr.perAttemptFor(class), NoProgress: noProg, AbsoluteCap: cap}
}

// WaitCapped builds a CAP-ONLY PollConfig (NoProgress disabled): wait until cond
// is ready or the generous cap elapses. Use for BINARY/EDGE readiness (ssh-up,
// boot_id, cloud-init-done, file-present, channel-up) where the marker is frozen
// until the end, AND for AUTHOR/CALLER-declared deadlines (eventually:,
// WaitSeconds, guest-exec maxWait) which must be preserved exactly. cap<=0 falls
// back to the generous config absolute_cap.
func (rr ResolvedReadiness) WaitCapped(name string, class PollClass, cap time.Duration) PollConfig {
	if cap <= 0 {
		cap = rr.AbsoluteCap
		if cap <= 0 {
			cap = readinessAbsoluteCapFallback
		}
	}
	return PollConfig{Name: name, Interval: rr.interval(class), PerAttempt: rr.perAttemptFor(class), NoProgress: 0, AbsoluteCap: cap}
}

// WatchProgress builds a NO-CAP monotonic watchdog (NoProgress active, no
// absolute cap) for an intentionally unbounded long-runner: the AI iteration
// loop, whose run must NOT be killed at a wall-clock cap as long as the score
// keeps improving. noProgress<=0 falls back to the config no_progress.
func (rr ResolvedReadiness) WatchProgress(name string, class PollClass, noProgress time.Duration) PollConfig {
	if noProgress <= 0 {
		noProgress = rr.NoProgress
		if noProgress <= 0 {
			noProgress = readinessNoProgressFallback
		}
	}
	return PollConfig{Name: name, Interval: rr.interval(class), PerAttempt: rr.perAttemptFor(class), NoProgress: noProgress, AbsoluteCap: 0}
}

// StopGate builds a settle/teardown PollConfig: cap-only at StopGrace (a clean
// timeout for the binary "is it down yet?" — the caller performs the force
// fallback on ErrPollCapExceeded). Replaces the 60s/180s/10s stop magic numbers
// with one config-sourced grace; no 2× multiplier.
func (rr ResolvedReadiness) StopGate(name string) PollConfig {
	grace := rr.StopGrace
	if grace <= 0 {
		grace = readinessStopGraceFallback
	}
	return PollConfig{Name: name, Interval: rr.interval(PollLocal), PerAttempt: rr.perAttempt(), NoProgress: 0, AbsoluteCap: grace}
}
