package main

import (
	"fmt"
	"maps"
	"regexp"
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

	// Captures is the plan-run-local stash populated by checks with
	// `capture:` set. Keys are the unprefixed capture names (e.g.
	// `login_tab`); the variable resolver looks them up under
	// `CAPTURED:<name>` so the existing ${NAME:arg} grammar in
	// testspec.go does the substitution for free.
	Captures map[string]string

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

// NewScenarioContext returns an empty plan-run context. Count
// expansions each get their own context so captured values from one
// iteration don't leak into the next.
func NewScenarioContext() *ScenarioContext {
	return &ScenarioContext{
		Captures: map[string]string{},
		Results:  map[string]CheckResult{},
	}
}

// Capture stores a value produced by a passing check under `capture:`.
// Called only on PASS (failing `eventually:` attempts must not pollute).
// Empty name / empty value is a no-op — authoring ergonomics: writing
// `capture:` on a check that didn't actually produce output shouldn't
// crash the run.
// Thread-safe: parallel-grouped steps may capture concurrently.
func (s *ScenarioContext) Capture(name, value string) {
	if s == nil || name == "" || value == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Captures == nil {
		s.Captures = map[string]string{}
	}
	s.Captures[name] = value
}

// Get returns the captured value for a name, or ("", false) if unset.
// Used by the variable resolver's CAPTURED:<name> branch.
// Thread-safe.
func (s *ScenarioContext) Get(name string) (string, bool) {
	if s == nil {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.Captures[name]
	return v, ok
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

// RecordResult stores a step's CheckResult for later inspection by
// `summarize:` verbs. Keyed by step ID.
func (s *ScenarioContext) RecordResult(stepID string, r CheckResult) {
	if s == nil || stepID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Results == nil {
		s.Results = map[string]CheckResult{}
	}
	s.Results[stepID] = r
}

// SnapshotResults returns a copy of all recorded results. Used by
// `summarize:` to compute aggregate metrics.
func (s *ScenarioContext) SnapshotResults() map[string]CheckResult {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]CheckResult, len(s.Results))
	maps.Copy(out, s.Results)
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
//   - CAPTURED:<name>      → value stored via Capture(name, value)
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
	for name, value := range s.Captures {
		key := "CAPTURED:" + name
		if _, exists := env[key]; !exists {
			env[key] = value
		}
	}
}

// ExtractCaptureValue picks the natural captured value from a check's
// runtime output. Each verb has an obvious capture target (command
// stdout, http body, cdp method output, etc.); this function
// centralises the mapping so capture semantics stay consistent across
// verbs.
//
// Today's heuristic: use the CheckResult's Message field. Verb handlers
// overwrite Message with their primary output on PASS (for example
// `runCommand` includes stdout in the Message; `runCDP` in
// `testrun_ov_verbs.go` puts captured subprocess stdout there). The
// caller (Runner) holds the stdout verbatim for verbs where Message
// is a summary rather than raw output and supplies that instead.
//
// Callers should prefer CaptureFromResult when they have a CheckResult
// and Captures already knows the raw value (e.g. command stdout); it
// falls back to Result.Message for verbs that don't carry the raw
// value separately.
func CaptureFromResult(rawValue, resultMessage string) string {
	if rawValue != "" {
		return rawValue
	}
	return resultMessage
}

// ApplyCaptureExtract runs the regex `pattern` against `value` and
// returns the first submatch group (or the whole match if no groups
// exist). Returns an error if the pattern is invalid or doesn't match.
//
// Used by the runner when a check sets `capture_extract:` alongside
// `capture:` — see checkrun.go's post-dispatch capture block. A failed
// match deliberately surfaces as an error so the caller can FAIL the
// check rather than silently store the unextracted (noisy) value.
func ApplyCaptureExtract(value, pattern string) (string, error) {
	if pattern == "" {
		return value, nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", fmt.Errorf("invalid capture_extract regex %q: %w", pattern, err)
	}
	m := re.FindStringSubmatch(value)
	if m == nil {
		return "", fmt.Errorf("capture_extract regex %q did not match input", pattern)
	}
	if len(m) >= 2 {
		return m[1], nil
	}
	return m[0], nil
}
