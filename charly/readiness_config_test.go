package main

import (
	"strings"
	"testing"
	"time"
)

func TestReadinessConfig_ResolveDefaults(t *testing.T) {
	rr, err := readinessResolve(nil)
	if err != nil {
		t.Fatalf("nil/default resolve must succeed: %v", err)
	}
	if rr.NoProgress != readinessNoProgressFallback || rr.AbsoluteCap != readinessAbsoluteCapFallback || rr.StopGrace != readinessStopGraceFallback {
		t.Fatalf("defaults wrong: %+v", rr)
	}
	if err := rr.ValidateOrdering(); err != nil {
		t.Fatalf("default ordering must be valid: %v", err)
	}
}

func TestReadinessConfig_ParseError(t *testing.T) {
	_, err := readinessResolve(&ReadinessConfig{NoProgress: "ninety"})
	if err == nil || !strings.Contains(err.Error(), "no_progress") {
		t.Fatalf("bad duration must error, got %v", err)
	}
}

func TestReadinessConfig_OrderingRejected(t *testing.T) {
	// poll_interval_heavy (120s) > no_progress (90s default) — the env-bypass case.
	_, err := readinessResolve(&ReadinessConfig{PollIntervalHeavy: "120s"})
	if err == nil || !strings.Contains(err.Error(), "no_progress") {
		t.Fatalf("interval>no_progress must be rejected, got %v", err)
	}
	// stop_grace > absolute_cap.
	_, err = readinessResolve(&ReadinessConfig{StopGrace: "40m"})
	if err == nil || !strings.Contains(err.Error(), "absolute_cap") {
		t.Fatalf("stop_grace>absolute_cap must be rejected, got %v", err)
	}
}

func TestReadinessConfig_ValidOverride(t *testing.T) {
	rr, err := readinessResolve(&ReadinessConfig{NoProgress: "5m", AbsoluteCap: "1h"})
	if err != nil {
		t.Fatalf("valid override: %v", err)
	}
	if rr.NoProgress != 5*time.Minute || rr.AbsoluteCap != time.Hour {
		t.Fatalf("override not applied: %+v", rr)
	}
}
