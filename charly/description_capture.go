package main

import (
	"sync"
)

// ScenarioContext carries per-plan-run mutable state across the
// execution of that run's steps — principally the capture store
// populated by checks with `capture: <name>`. Instantiated fresh per
// plan run (and per count expansion) so cross-run state never leaks.
//
// The struct also threads the current step identifier through variable
// expansion (${STEP_ID}) so artifact paths and narrative text can embed
// stable references without the runner having to know about them.

// A ScenarioContext is OWNED by one plan run's execution pass. When
// that pass completes, the context is discarded; the next run gets a
// fresh one. `capture:` values never survive the run that produced them.
type ScenarioContext struct {
	// CurrentStepID is rewritten for each step as the plan run executes,
	// so ${STEP_ID} references resolve to the currently-running step's
	// identifier. Reporters surface this on failures.
	CurrentStepID string

	// mu guards Captures + Backgrounds + Results when steps execute
	// concurrently within a `parallel:` group. The default (sequential)
	// path doesn't contend, so the mutex overhead is negligible.
	mu sync.Mutex

	// Backgrounds tracks PIDs of host-side processes spawned by
	// `command:` verbs with `background: true`. Reaped at plan
	// teardown via SIGTERM (best-effort; non-fatal on failure).
	Backgrounds []int

	// Results accumulates CheckResults from steps that have completed,
	// indexed by step ID. Used by the `summarize:` verb to walk prior
	// steps' Elapsed durations and compute distribution metrics.
	Results map[string]CheckResult
}

// NewScenarioContext returns an empty plan-run context.
func NewScenarioContext() *ScenarioContext {
	return &ScenarioContext{
		Results: map[string]CheckResult{},
	}
}

// AddBackground records a PID for later teardown reaping. Thread-safe.
func (s *ScenarioContext) AddBackground(pid int) {
	if s == nil || pid <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Backgrounds = append(s.Backgrounds, pid)
}

// SnapshotBackgrounds returns a copy of the current backgrounds slice.
// Used by the teardown reaper.
func (s *ScenarioContext) SnapshotBackgrounds() []int {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]int, len(s.Backgrounds))
	copy(out, s.Backgrounds)
	return out
}

// ApplyToEnv merges plan-run-scope variables into an env map for
// variable expansion. Called by the runner immediately before
// `Check.ExpandVars(env)` so the existing `${NAME[:arg]}` grammar
// picks up captures and the step id without knowing about the
// ScenarioContext type.
//
// Keys populated:
//
//   - STEP_ID              → ctx.CurrentStepID
//
// ApplyToEnv overlays — it never overwrites existing keys so a
// host-level `${STEP_ID}` override (if ever introduced for testing)
// continues to win. The runner builds env by copying its resolver's
// base env first and then calling ApplyToEnv on the copy.
func (s *ScenarioContext) ApplyToEnv(env map[string]string) {
	if s == nil || env == nil {
		return
	}
	if _, exists := env["STEP_ID"]; !exists && s.CurrentStepID != "" {
		env["STEP_ID"] = s.CurrentStepID
	}
}
