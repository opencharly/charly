package main

// harness_watchdog.go — score-progress watchdog for the AI runner.
//
// Round 3 of the harness composition cutover replaced the hardcoded
// 30-minute per-iteration wall-clock cap (which contradicted the
// score's "Take all the time you need" prompt) with this watchdog,
// which bounds each iteration by SCORING PROGRESS rather than wall
// clock.
//
// Behavior per the spec:
//
//   1. Every CheckInterval (default 5 min), Run probes the live
//      deployments via the supplied Prober. The prober returns the
//      current (score, total) snapshot — typically by calling
//      RunCheckLive against the iter's in-scope plan steps.
//   2. OnTick fires after every probe with the current observation
//      (host-side stderr logging, NOT into any AI-visible surface).
//   3. If the score has not increased in NoImprovementTimeout (default
//      30 min), Run invokes OnTimeout with a reason string. Callers
//      typically wire OnTimeout to cancel the runner's context, which
//      terminates the AI subprocess.
//
// The watchdog is HIDDEN from the AI by construction:
//   - Runs in the harness Go process, not in any tool the AI invokes.
//   - Adds no token to the prompt, no field to ${PLAN}/${CHECKS},
//     no entry in `charly check scope`, no log line in NOTES.md.
//   - The AI's view of the iteration is unchanged from before Round 3.
//
// `Run` exits cleanly when the runner's context is cancelled (the
// common case: the AI subprocess returned and the caller called
// cancelRunner()).

import (
	"context"
	"fmt"
	"time"
)

// Prober is the function signature the watchdog uses to sample the
// current score. Concrete implementations call RunCheckLive
// against the iter's plan steps; tests pass a fake.
//
// Returns (score, total, err). On err, Run logs the error via
// OnTickError (or skips the tick) but does NOT count it as
// "no progress" — a transient probe failure (container missing during
// AI rebuild, podman lock contention, etc.) shouldn't trigger a false
// timeout.
type Prober func(ctx context.Context) (score, total int, err error)

// ProgressWatchdog ticks at CheckInterval, samples the current score
// via Prober, fires OnTick for observability, and fires OnTimeout
// when the score has not increased in NoImprovementTimeout.
type ProgressWatchdog struct {
	CheckInterval        time.Duration // default 5m (caller applies)
	NoImprovementTimeout time.Duration // default 30m (caller applies)

	// BenchmarkStart anchors all user-facing time displays to the
	// benchmark's run-start instant. When set (the harness_loop.go
	// caller passes the run's `started` time here), every absolute
	// timestamp the watchdog formats becomes a `+Nm0s` offset from
	// this anchor. Pre-2026-04 the watchdog formatted clock times
	// (HH:MM:SS) which forced operators to compute deltas mentally
	// against the run start. When zero, the watchdog falls back to
	// per-iteration "iter started at HH:MM:SS" framing.
	BenchmarkStart time.Time

	// Probe samples the current iter score. Required.
	Probe Prober

	// OnTick fires after every probe with the current observation.
	// Optional. Receives wall-clock elapsed since Run was called,
	// the (score, total) snapshot, and the timestamp of the last
	// score increase (zero-valued if the score has never improved).
	OnTick func(elapsed time.Duration, score, total int, lastImprovedAt time.Time)

	// OnTickError fires when Probe returns an error. Optional. The
	// reason is the error string. Callers typically log to stderr.
	OnTickError func(err error)

	// OnTimeout fires when the score has not improved in
	// NoImprovementTimeout. Required for the watchdog to be useful;
	// callers typically wire it to cancel the runner's context.
	OnTimeout func(reason string)
}

// Run blocks until ctx is done OR OnTimeout has fired. After OnTimeout
// fires, Run continues to ctx.Done() so the goroutine exits cleanly
// alongside the cancelled runner.
//
// If NoImprovementTimeout <= 0, the watchdog never times out — it just
// emits OnTick for observability. If CheckInterval <= 0, Run returns
// immediately (watchdog disabled).
func (w *ProgressWatchdog) Run(ctx context.Context) {
	if w.CheckInterval <= 0 {
		return
	}
	ticker := time.NewTicker(w.CheckInterval)
	defer ticker.Stop()

	start := time.Now()
	bestScore := -1 // sentinel: no probe yet
	var lastImprovedAt time.Time
	timedOut := false

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			elapsed := time.Since(start)
			score, total, err := w.Probe(ctx)
			if err != nil {
				if w.OnTickError != nil {
					w.OnTickError(err)
				}
				// Probe failure does NOT advance the no-improvement
				// timer's "last improved" — it's neither progress nor
				// regress. We just skip this tick.
				continue
			}
			// First probe ever (sentinel bestScore == -1): RECORD the
			// baseline but do NOT call it an improvement. Otherwise a
			// run that opens with score 0 would emit
			// "last improvement 0s ago" at the very first tick — which
			// is technically true (0 > -1) but semantically wrong; the
			// score has not improved, the watchdog has just observed
			// the baseline. Same trap fires when a phase boundary
			// preserves passing steps from earlier phases: the
			// first probe of the new phase sees a non-zero score that
			// reflects PRIOR work, not improvement during this iter.
			// Treating the first probe as baseline-only fixes both.
			if bestScore < 0 {
				bestScore = score
			} else if score > bestScore {
				bestScore = score
				lastImprovedAt = time.Now()
			}
			if w.OnTick != nil {
				w.OnTick(elapsed, score, total, lastImprovedAt)
			}
			// Timeout check — only after at least one improvement
			// recorded. If lastImprovedAt is zero (the AI has never
			// scored anything), wait until elapsed exceeds
			// NoImprovementTimeout to avoid trivial-zero-baseline races.
			if timedOut || w.NoImprovementTimeout <= 0 || w.OnTimeout == nil {
				continue
			}
			var idle time.Duration
			if lastImprovedAt.IsZero() {
				idle = elapsed
			} else {
				idle = time.Since(lastImprovedAt)
			}
			if idle >= w.NoImprovementTimeout {
				reason := fmt.Sprintf(
					"no scoring progress for %s (last improvement %s, current score %d/%d)",
					idle.Round(time.Second),
					w.lastImprovedSuffix(lastImprovedAt, start),
					score, total,
				)
				w.OnTimeout(reason)
				timedOut = true
				// Don't return — let ctx.Done() unwind us cleanly so
				// the caller's cancelRunner() takes effect first.
			}
		}
	}
}

// lastImprovedSuffix renders the lastImprovedAt timestamp relative to
// the benchmark start when w.BenchmarkStart is set, e.g. "at +13m45s
// since benchmark start". When BenchmarkStart is zero, falls back to
// the legacy "at HH:MM:SS" / "iteration started at HH:MM:SS" format.
//
// Post-2026-04 the harness emits ALL user-facing time displays as
// run-relative offsets so operators can read "how far into the run did
// X happen?" without computing deltas against wall clocks.
func (w *ProgressWatchdog) lastImprovedSuffix(lastImprovedAt, start time.Time) string {
	if !w.BenchmarkStart.IsZero() {
		if lastImprovedAt.IsZero() {
			return fmt.Sprintf("never observed (iter started +%s into the run)",
				start.Sub(w.BenchmarkStart).Round(time.Second))
		}
		return fmt.Sprintf("at +%s into the run",
			lastImprovedAt.Sub(w.BenchmarkStart).Round(time.Second))
	}
	if lastImprovedAt.IsZero() {
		return fmt.Sprintf("never observed (iteration started at %s)",
			start.Format("15:04:05"))
	}
	return "at " + lastImprovedAt.Format("15:04:05")
}
