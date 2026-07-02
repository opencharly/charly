package vmshared

import (
	"fmt"
	"os"
	"time"
)

// readiness_resolve.go — the SINGLE config→resolved readiness resolver, shared by charly
// core AND the out-of-process plugins (both alias ResolveReadiness via their vmshared_aliases).
// The ResolvedReadiness type + the readiness*Fallback consts live in poll.go; ReadinessConfig
// is the `defaults.readiness:` block. Every poll bound is NAMED (the fallbacks), CONFIG-SOURCED
// (ReadinessConfig), env-overridable (CHARLY_READINESS_*), and VALIDATED on the RESOLVED
// post-env values (ValidateOrdering) — an env override cannot smuggle in a nonsensical bound.

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
