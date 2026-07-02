package main

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// runSummarize implements the `summarize:` verb (Path B from the
// concurrency-test plan). It walks the plan-run context's recorded
// CheckResults, filters by step-id glob patterns in OverIDs, and
// computes distribution metrics (p50/p95/p99/max/mean) over each
// matching step's Elapsed time. Optional per-metric matchers
// (P50Match, P95Match, P99Match, MaxMatch, MeanMatch) assert
// thresholds — the verb fails if any threshold is exceeded.
//
// Scope: walks ONLY the current plan run's results (via r.Scenario).
// summarize verbs invoked without a plan-run context skip with a clear
// message.
//
//nolint:gocyclo,unparam // linear filter→sort→compute→validate pipeline; metric switch incidental; ctx unused (uniform verb-handler signature shared with the other runX(ctx, *Op) handlers)
func (r *Runner) runSummarize(ctx context.Context, c *Op) CheckResult {
	if r.Scenario == nil {
		return skipf(c, "summarize: requires a plan-run context (use inside plan steps)")
	}
	if len(c.OverIDs) == 0 {
		return failf(c, "summarize: over_ids: must list at least one step-id glob")
	}

	// Walk recorded results, filter by glob match.
	results := r.Scenario.SnapshotResults()
	var samples []time.Duration
	for stepID, checkRes := range results {
		for _, glob := range c.OverIDs {
			matched, err := filepath.Match(glob, stepID)
			if err == nil && matched {
				samples = append(samples, checkRes.Elapsed)
				break
			}
		}
	}

	if len(samples) == 0 {
		return failf(c, "summarize: no recorded steps matched any of %v", c.OverIDs)
	}

	// Sort for percentile computation.
	slices.Sort(samples)

	// Compute requested metrics. Default to all five if none specified.
	metricSet := c.Metrics
	if len(metricSet) == 0 {
		metricSet = []string{"p50", "p95", "p99", "max", "mean"}
	}
	values := map[string]float64{}
	for _, m := range metricSet {
		switch strings.ToLower(m) {
		case "p50":
			values["p50"] = pctMS(samples, 0.50)
		case "p95":
			values["p95"] = pctMS(samples, 0.95)
		case "p99":
			values["p99"] = pctMS(samples, 0.99)
		case "max":
			values["max"] = float64(samples[len(samples)-1].Microseconds()) / 1000.0
		case "mean":
			var total time.Duration
			for _, s := range samples {
				total += s
			}
			values["mean"] = float64(total.Microseconds()) / 1000.0 / float64(len(samples))
		default:
			return failf(c, "summarize: unknown metric %q (allowed: p50, p95, p99, max, mean)", m)
		}
	}

	// Apply per-metric matchers if set.
	violations := []string{}
	checkMetric := func(name string, m Matcher) {
		if m.Op == "" {
			return
		}
		v, ok := values[name]
		if !ok {
			return
		}
		// Compare against the matcher's numeric value.
		threshold, ok := matcherNumeric(m)
		if !ok {
			violations = append(violations, fmt.Sprintf("%s matcher %q has non-numeric value %v", name, m.Op, m.Value))
			return
		}
		switch m.Op {
		case "lt":
			if !(v < threshold) {
				violations = append(violations, fmt.Sprintf("%s=%.2fms NOT lt %.2fms", name, v, threshold))
			}
		case "le":
			if !(v <= threshold) {
				violations = append(violations, fmt.Sprintf("%s=%.2fms NOT le %.2fms", name, v, threshold))
			}
		case "gt":
			if !(v > threshold) {
				violations = append(violations, fmt.Sprintf("%s=%.2fms NOT gt %.2fms", name, v, threshold))
			}
		case "ge":
			if !(v >= threshold) {
				violations = append(violations, fmt.Sprintf("%s=%.2fms NOT ge %.2fms", name, v, threshold))
			}
		case "equals":
			if math.Abs(v-threshold) > 0.001 {
				violations = append(violations, fmt.Sprintf("%s=%.2fms NOT equals %.2fms", name, v, threshold))
			}
		default:
			violations = append(violations, fmt.Sprintf("%s matcher op %q unsupported (use lt/le/gt/ge/equals)", name, m.Op))
		}
	}
	checkMetric("p50", c.P50Match)
	checkMetric("p95", c.P95Match)
	checkMetric("p99", c.P99Match)
	checkMetric("max", c.MaxMatch)
	checkMetric("mean", c.MeanMatch)

	// Build the summary message — pasted into the CheckResult so reporters
	// surface it.
	parts := []string{fmt.Sprintf("n=%d", len(samples))}
	for _, m := range []string{"p50", "p95", "p99", "max", "mean"} {
		if v, ok := values[m]; ok {
			parts = append(parts, fmt.Sprintf("%s=%.2fms", m, v))
		}
	}
	summary := strings.Join(parts, " ")

	if len(violations) > 0 {
		return failf(c, "summarize: %s; violations: %s", summary, strings.Join(violations, "; "))
	}
	return passf(c, "summarize: "+summary)
}

// pctMS returns the percentile-p value of the (sorted) sample slice in
// milliseconds. p must be in [0, 1].
func pctMS(sorted []time.Duration, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := max(int(math.Round(p*float64(len(sorted)-1))), 0)
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return float64(sorted[idx].Microseconds()) / 1000.0
}

// matcherNumeric extracts a float64 from a Matcher's value field for
// numeric comparisons. Returns (0, false) if the value isn't numeric.
func matcherNumeric(m Matcher) (float64, bool) {
	switch v := m.Value.(type) {
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case string:
		// Try parsing as a float (allows authors to write `lt: "5000"`).
		var f float64
		_, err := fmt.Sscanf(v, "%f", &f)
		return f, err == nil
	}
	return 0, false
}
