package check

// watchdog.go — score-progress watchdog for the AI runner (P12: relocated verbatim
// from charly/check_watchdog.go — pure; the injected Prober body drives the "score"
// check-run mode in the plugin instead of an in-core RunCheckLive).
//
// Bounds each iteration by SCORING PROGRESS rather than wall clock:
//
//  1. Every CheckInterval (default 5 min), Run probes the live deployments via the
//     supplied Prober (typically the "score" host seam against the iter's in-scope
//     plan steps).
//  2. OnTick fires after every probe with the current observation (host-side stderr
//     logging, NOT into any AI-visible surface).
//  3. If the score has not increased in NoImprovementTimeout (default 30 min), Run
//     invokes OnTimeout with a reason string. Callers wire OnTimeout to cancel the
//     runner's context, which terminates the AI subprocess.
//
// The watchdog is HIDDEN from the AI by construction: it runs in the harness Go
// process, adds no token to the prompt, and appears in no tool the AI invokes.

import (
	"context"
	"fmt"
	"time"
)

// Prober is the function signature the watchdog uses to sample the current score.
// Returns (score, total, err). On err, Run logs via OnTickError (or skips the tick)
// but does NOT count it as "no progress" — a transient probe failure shouldn't
// trigger a false timeout.
type Prober func(ctx context.Context) (score, total int, err error)

// ProgressWatchdog ticks at CheckInterval, samples the current score via Prober,
// fires OnTick for observability, and fires OnTimeout when the score has not
// increased in NoImprovementTimeout.
type ProgressWatchdog struct {
	CheckInterval        time.Duration // default 5m (caller applies)
	NoImprovementTimeout time.Duration // default 30m (caller applies)

	// BenchmarkStart anchors all user-facing time displays to the benchmark's
	// run-start instant. When set, every absolute timestamp the watchdog formats
	// becomes a `+Nm0s` offset from this anchor.
	BenchmarkStart time.Time

	// Probe samples the current iter score. Required.
	Probe Prober

	// OnTick fires after every probe with the current observation. Optional.
	OnTick func(elapsed time.Duration, score, total int, lastImprovedAt time.Time)

	// OnTickError fires when Probe returns an error. Optional.
	OnTickError func(err error)

	// OnTimeout fires when the score has not improved in NoImprovementTimeout.
	// Required for the watchdog to be useful; callers wire it to cancel the
	// runner's context.
	OnTimeout func(reason string)
}

// Run blocks until ctx is done OR OnTimeout has fired. After OnTimeout fires, Run
// continues to ctx.Done() so the goroutine exits cleanly alongside the cancelled
// runner.
//
// If NoImprovementTimeout <= 0, the watchdog never times out — it just emits OnTick.
// If CheckInterval <= 0, Run returns immediately (watchdog disabled).
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
				// Probe failure does NOT advance the no-improvement timer's "last
				// improved" — it's neither progress nor regress. Skip this tick.
				continue
			}
			// First probe ever (sentinel bestScore == -1): RECORD the baseline but
			// do NOT call it an improvement. Same trap fires when a phase boundary
			// preserves passing steps from earlier phases.
			if bestScore < 0 {
				bestScore = score
			} else if score > bestScore {
				bestScore = score
				lastImprovedAt = time.Now()
			}
			if w.OnTick != nil {
				w.OnTick(elapsed, score, total, lastImprovedAt)
			}
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
				// Don't return — let ctx.Done() unwind us cleanly so the caller's
				// cancelRunner() takes effect first.
			}
		}
	}
}

// lastImprovedSuffix renders the lastImprovedAt timestamp relative to the benchmark
// start when w.BenchmarkStart is set; otherwise falls back to the legacy HH:MM:SS
// format.
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
