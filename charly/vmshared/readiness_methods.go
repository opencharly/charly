package vmshared

import (
	"fmt"
	"time"
)

// readiness_methods.go — exported ResolvedReadiness methods needed across the
// package boundary.
//
//   - PerAttemptFor is the exported accessor for the effective per-attempt
//     never-hang bound of a poll class (the unexported perAttempt/perAttemptHeavy
//     accessors collide on export with the PerAttempt/PerAttemptHeavy struct
//     fields, so consumers reach the bound through this class-keyed facade).
//   - ValidateOrdering moved here when ResolvedReadiness became a shared type: it
//     was previously a method defined on ResolvedReadiness in each module's
//     readiness_config.go, byte-for-byte identical in both — a method can only be
//     declared in the type's own package, so it lives here once.

// PerAttemptFor returns the effective per-attempt never-hang bound for a poll
// class (the field value, or the built-in fallback when unset).
func (rr ResolvedReadiness) PerAttemptFor(class PollClass) time.Duration {
	return rr.perAttemptFor(class)
}

// ValidateOrdering enforces the bound ordering on the RESOLVED set (must run
// post-env, not on the raw YAML — an env override must not slip a bad bound
// through): each interval <= no_progress; no_progress <= absolute_cap;
// poll_interval_local <= stop_grace <= absolute_cap.
func (rr ResolvedReadiness) ValidateOrdering() error {
	for _, iv := range []struct {
		n string
		d time.Duration
	}{
		{"poll_interval_local", rr.IntervalLocal},
		{"poll_interval_remote", rr.IntervalRemote},
		{"poll_interval_heavy", rr.IntervalHeavy},
	} {
		if iv.d > rr.NoProgress {
			return fmt.Errorf("readiness: %s (%s) must be <= no_progress (%s)", iv.n, iv.d, rr.NoProgress)
		}
	}
	if rr.NoProgress > rr.AbsoluteCap {
		return fmt.Errorf("readiness: no_progress (%s) must be <= absolute_cap (%s)", rr.NoProgress, rr.AbsoluteCap)
	}
	// per_attempt (one atomic probe) <= per_attempt_heavy (a whole multi-probe
	// pass) <= absolute_cap (the readiness-retry ceiling). A heavy per-attempt
	// longer than the absolute cap would allow only a single attempt with no
	// retry budget; one shorter than per_attempt is incoherent.
	if rr.PerAttempt > rr.PerAttemptHeavy {
		return fmt.Errorf("readiness: per_attempt (%s) must be <= per_attempt_heavy (%s)", rr.PerAttempt, rr.PerAttemptHeavy)
	}
	if rr.PerAttemptHeavy > rr.AbsoluteCap {
		return fmt.Errorf("readiness: per_attempt_heavy (%s) must be <= absolute_cap (%s)", rr.PerAttemptHeavy, rr.AbsoluteCap)
	}
	if rr.StopGrace > rr.AbsoluteCap {
		return fmt.Errorf("readiness: stop_grace (%s) must be <= absolute_cap (%s)", rr.StopGrace, rr.AbsoluteCap)
	}
	if rr.StopGrace < rr.IntervalLocal {
		return fmt.Errorf("readiness: stop_grace (%s) must be >= poll_interval_local (%s)", rr.StopGrace, rr.IntervalLocal)
	}
	return nil
}
