package spec

import (
	"fmt"
	"os"
	"time"
)

// readiness.go — the exported ResolvedReadiness methods + the SINGLE
// config→resolved readiness resolver, shared by charly core AND the
// out-of-process plugins. The ResolvedReadiness type + the readiness*Fallback
// consts live in poll.go; ReadinessConfig is the `defaults.readiness:` block
// (generated wire type). Every poll bound is NAMED (the fallbacks), CONFIG-SOURCED
// (ReadinessConfig), env-overridable (CHARLY_READINESS_*), and VALIDATED on the
// RESOLVED post-env values (ValidateOrdering) — an env override cannot smuggle in
// a nonsensical bound. Relocated from sdk/vmshared into the floor-legal spec leaf
// so kernel-floor files reach these bounds without a vmshared import.

// PerAttemptFor returns the effective per-attempt never-hang bound for a poll
// class (the field value, or the built-in fallback when unset). The unexported
// perAttempt/perAttemptHeavy accessors collide on export with the
// PerAttempt/PerAttemptHeavy struct fields, so consumers reach the bound through
// this class-keyed facade.
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

// resolveReadinessDuration: env (CHARLY_READINESS_*) wins over the config string, which wins
// over the named fallback. Parses + rejects non-positive.
func resolveReadinessDuration(field, envKey, cfgVal string, fallback time.Duration) (time.Duration, error) {
	raw := cfgVal
	if e := os.Getenv(envKey); e != "" {
		raw = e
	}
	if raw == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("readiness.%s: invalid duration %q: %w", field, raw, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("readiness.%s: must be > 0, got %s", field, d)
	}
	return d, nil
}

// readinessSpec is one readiness field: its config-key name, its CHARLY_READINESS_* env override,
// its built-in fallback, and accessors for the config value (in) and the resolved field pointer
// (out). readinessSpecs is the SINGLE field-set source: ResolveReadiness READS it (config/env/
// fallback → resolved); ResolvedReadiness.PluginEnv WRITES it (resolved → env).
type readinessSpec struct {
	name, env string
	fb        time.Duration
	cfg       func(*ReadinessConfig) string
	dst       func(*ResolvedReadiness) *time.Duration
}

var readinessSpecs = []readinessSpec{
	{"poll_interval_local", "CHARLY_READINESS_POLL_LOCAL", readinessIntervalLocalFallback, func(rc *ReadinessConfig) string { return rc.PollIntervalLocal }, func(rr *ResolvedReadiness) *time.Duration { return &rr.IntervalLocal }},
	{"poll_interval_remote", "CHARLY_READINESS_POLL_REMOTE", readinessIntervalRemoteFallback, func(rc *ReadinessConfig) string { return rc.PollIntervalRemote }, func(rr *ResolvedReadiness) *time.Duration { return &rr.IntervalRemote }},
	{"poll_interval_heavy", "CHARLY_READINESS_POLL_HEAVY", readinessIntervalHeavyFallback, func(rc *ReadinessConfig) string { return rc.PollIntervalHeavy }, func(rr *ResolvedReadiness) *time.Duration { return &rr.IntervalHeavy }},
	{"per_attempt", "CHARLY_READINESS_PER_ATTEMPT", readinessPerAttemptFallback, func(rc *ReadinessConfig) string { return rc.PerAttempt }, func(rr *ResolvedReadiness) *time.Duration { return &rr.PerAttempt }},
	{"per_attempt_heavy", "CHARLY_READINESS_PER_ATTEMPT_HEAVY", readinessPerAttemptHeavyFallback, func(rc *ReadinessConfig) string { return rc.PerAttemptHeavy }, func(rr *ResolvedReadiness) *time.Duration { return &rr.PerAttemptHeavy }},
	{"no_progress", "CHARLY_READINESS_NO_PROGRESS", readinessNoProgressFallback, func(rc *ReadinessConfig) string { return rc.NoProgress }, func(rr *ResolvedReadiness) *time.Duration { return &rr.NoProgress }},
	{"absolute_cap", "CHARLY_READINESS_ABSOLUTE_CAP", readinessAbsoluteCapFallback, func(rc *ReadinessConfig) string { return rc.AbsoluteCap }, func(rr *ResolvedReadiness) *time.Duration { return &rr.AbsoluteCap }},
	{"stop_grace", "CHARLY_READINESS_STOP_GRACE", readinessStopGraceFallback, func(rc *ReadinessConfig) string { return rc.StopGrace }, func(rr *ResolvedReadiness) *time.Duration { return &rr.StopGrace }},
}

// ResolveReadiness materializes the validated ResolvedReadiness from a `defaults.readiness:`
// block plus CHARLY_READINESS_* env overrides plus the named fallbacks. Nil-safe (nil → all
// fallbacks/env). The SINGLE resolver — charly core (loadedReadiness, from the project config)
// AND the out-of-process plugins (nil + the host-threaded env) both call it.
func ResolveReadiness(rc *ReadinessConfig) (ResolvedReadiness, error) {
	if rc == nil {
		rc = &ReadinessConfig{}
	}
	var rr ResolvedReadiness
	for _, s := range readinessSpecs {
		d, err := resolveReadinessDuration(s.name, s.env, s.cfg(rc), s.fb)
		if err != nil {
			return ResolvedReadiness{}, err
		}
		*s.dst(&rr) = d
	}
	if err := rr.ValidateOrdering(); err != nil {
		return ResolvedReadiness{}, err
	}
	return rr, nil
}

// PluginEnv emits the CHARLY_READINESS_* entries (KEY=VALUE) for this RESOLVED readiness, so an
// out-of-process plugin — which cannot LoadUnified — re-reads the host-resolved bounds via its
// own ResolveReadiness. The host appends these to a plugin's spawn env (charly's
// LocalTransport.Connect). Durations round-trip through String() ⟷ resolveReadinessDuration.
func (rr ResolvedReadiness) PluginEnv() []string {
	out := make([]string, 0, len(readinessSpecs))
	for _, s := range readinessSpecs {
		out = append(out, s.env+"="+s.dst(&rr).String())
	}
	return out
}
