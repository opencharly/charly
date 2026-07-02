package main

// check_loop.go — the iteration state machine for `charly check run`.
//
// Post the plan-unify cutover, the loop is keyed on an entity's `iterate:`
// block (IterateConfig) carried on a deploy / kind:check bed. Per iteration:
//   - the AI sees the full scored plan via ${PLAN} and the still-unsolved
//     check:/agent-check: subset via ${CHECKS}
//   - the harness scores the entity's plan check:/agent-check: STEPS —
//     score = total solved across the whole plan
//   - the prompt surfaces ${SCORE_DELTA} (improvement vs prev iter)
//     and ${ATTEMPTS_LEFT} (plateau_iteration - plateau_counter)
//
// The only loop bound is plateau detection. There is no max-iteration
// ceiling — as long as the AI keeps improving, the run continues.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// ---------------------------------------------------------------------------
// Opts + state types
// ---------------------------------------------------------------------------

// HarnessOpts are the resolved inputs to a run, populated by
// CheckRunLocalCmd.Run before calling RunHarness.
type HarnessOpts struct {
	ProjectDir string
	ScoreName  string // the iterate entity name (deploy / kind:check bed)
	Iterate    *IterateConfig

	// MergedPlan is the entity's scored plan (check:/agent-check: + agent-run:
	// + runtime-context run:), baked + include-expanded + inline, with any
	// ${EVAL_NONCE_*} placeholders UN-substituted. Drives ${PLAN}/${CHECKS}
	// prompt rendering — the slice the AI sees.
	MergedPlan []Step

	// ScoringPlan is MergedPlan with all ${EVAL_NONCE_*} tokens substituted to
	// their per-run hex values. Drives baseline synthesis AND per-iter scoring;
	// the AI never sees the substituted values.
	ScoringPlan []Step

	TargetKind string // "pod" | "vm" | "host"
	TargetName string // pod or vm name (empty when host)
	AgentName  string

	// Phase / PhaseTotal carry progressive-scoping context (0/0 = single-pass).
	Phase            int
	PhaseTotal       int
	Agent            *AgentConfig
	Prompt           string // template; per-iter substitution at render time
	TargetImage      string
	Tag              string
	PlateauIteration int
	MaxStep          int
	MCPEndpoint      string
	Notes            string // ${NOTES} snapshot at run start
	NoMCP            bool
	NoIsolate        bool
	DryRun           bool
	SkipRebuild      bool
	RebuildBaseline  bool
	Format           string // "text" | "yaml"
	Stdout           *os.File
	Stderr           *os.File

	// Deploy names the subject deployment the plan scores against (drives
	// ${DEPLOYMENT}). RunCheckLive scores by each step's Op.Pod, so this is
	// informational for the prompt.
	Deploy string

	// PreAIStep is the frozen set of scored steps. Populated from
	// synthesizeScoreBaseline at CheckRunLocalCmd entry.
	PreAIStep []StepScore

	// PreFingerprints maps step id -> body fingerprint at baseline.
	PreFingerprints map[string]string

	// PreTagFingerprints maps step id -> tag fingerprint at baseline.
	PreTagFingerprints map[string]string
}

// IterationState captures one iteration's outputs.
//
// For AIs with OutputFormat="" (plain), RunnerOutput inlines the FULL
// AI stdout/stderr from runner.log — no truncation. For AIs with
// OutputFormat="stream-json", stdout (NDJSON) is parsed line-by-line
// into RunnerEvent and stderr lands in a sibling file referenced by
// RunnerStderrPath; RunnerOutput is left empty in that mode because
// RunnerEvent is the structured equivalent. The raw NDJSON is also
// kept on disk at iter<k>/runner.ndjson for byte-exact debugging.
//
// StartedUTC / FinishedUTC / IterationDuration are absolute timestamps
// for the whole iteration body (build + runner + scoring) so a reader
// of result-{calver}.yml can reconstruct the wall-clock timeline.
// RunnerCommand captures the post-substitution argv that was actually
// exec'd (e.g. with ${PROMPT} expanded to the rendered prompt text).
//
// WatchdogSample is the score-progress timeline: one entry per
// CheckInterval tick (default 5m), each carrying (at_utc, elapsed,
// score, total, last_improved_at). This is what answers "what score
// did the AI reach when?" — cross-reference at_utc with StartedUTC.
type IterationState struct {
	K                   int              `yaml:"k" json:"k"`
	Phase               int              `yaml:"phase,omitempty" json:"phase,omitempty"`
	Score               int              `yaml:"score" json:"score"`
	ScoreDelta          int              `yaml:"score_delta" json:"score_delta"`
	PlateauCounterAfter int              `yaml:"plateau_counter_after" json:"plateau_counter_after"`
	BuildFailure        bool             `yaml:"build_failure,omitempty" json:"build_failure,omitempty"`
	StartedUTC          string           `yaml:"started_utc,omitempty" json:"started_utc,omitempty"`
	FinishedUTC         string           `yaml:"finished_utc,omitempty" json:"finished_utc,omitempty"`
	IterationDuration   string           `yaml:"iteration_duration,omitempty" json:"iteration_duration,omitempty"`
	BuildDuration       string           `yaml:"build_duration,omitempty" json:"build_duration,omitempty"`
	TestDuration        string           `yaml:"test_duration,omitempty" json:"test_duration,omitempty"`
	RunnerDuration      string           `yaml:"runner_duration,omitempty" json:"runner_duration,omitempty"`
	RunnerCommand       []string         `yaml:"runner_command,omitempty" json:"runner_command,omitempty"`
	RunnerOutput        string           `yaml:"runner_output,omitempty" json:"runner_output,omitempty"`
	RunnerLogPath       string           `yaml:"runner_log_path,omitempty" json:"runner_log_path,omitempty"`
	RunnerNdjsonPath    string           `yaml:"runner_ndjson_path,omitempty" json:"runner_ndjson_path,omitempty"`
	RunnerStderrPath    string           `yaml:"runner_stderr_path,omitempty" json:"runner_stderr_path,omitempty"`
	RunnerEvent         []RunnerEvent    `yaml:"runner_event,omitempty" json:"runner_event,omitempty"`
	WatchdogSample      []WatchdogSample `yaml:"watchdog_sample,omitempty" json:"watchdog_sample,omitempty"`
	BuildLogPath        string           `yaml:"build_log_path,omitempty" json:"build_log_path,omitempty"`
	CommitSHA           string           `yaml:"commit_sha,omitempty" json:"commit_sha,omitempty"`
	Step                []StepVerdict    `yaml:"step,omitempty" json:"step,omitempty"`
	AddedStep           []string         `yaml:"added_step,omitempty" json:"added_step,omitempty"`
}

// RunnerEvent is one parsed line from a stream-json AI runner's stdout.
// AtUTC is the wall-clock moment the line was read (RFC3339); Type is
// the top-level "type" field of the JSON object when present (claude
// emits "system", "assistant", "user", "result", etc.); Raw is the
// complete parsed JSON object so callers don't lose any fields. On a
// malformed JSON line the parser stores
// `Raw: {"_parse_error": <msg>, "_line": <raw bytes>}` and leaves Type
// empty — partial output survives rather than aborting the loop.
type RunnerEvent struct {
	AtUTC string         `yaml:"at_utc" json:"at_utc"`
	Type  string         `yaml:"type,omitempty" json:"type,omitempty"`
	Raw   map[string]any `yaml:"raw" json:"raw"`
}

// WatchdogSample is one tick of the score-progress watchdog (default
// 5m cadence). The harness loop appends one of these per OnTick fired
// during a stream-json or plain iteration that scores a live plan.
// LastImprovedAt is empty until the AI has scored at least once.
type WatchdogSample struct {
	AtUTC          string `yaml:"at_utc" json:"at_utc"`
	Elapsed        string `yaml:"elapsed" json:"elapsed"`
	Score          int    `yaml:"score" json:"score"`
	Total          int    `yaml:"total" json:"total"`
	LastImprovedAt string `yaml:"last_improved_at,omitempty" json:"last_improved_at,omitempty"`
}

// StepVerdict is one scored step's post-iteration outcome.
type StepVerdict struct {
	ID              string  `yaml:"id" json:"id"`
	Origin          string  `yaml:"origin,omitempty" json:"origin,omitempty"`
	Verdict         Verdict `yaml:"verdict" json:"verdict"`
	Baseline        string  `yaml:"baseline,omitempty" json:"baseline,omitempty"`
	Final           string  `yaml:"final,omitempty" json:"final,omitempty"`
	FingerprintPre  string  `yaml:"fingerprint_pre,omitempty" json:"fingerprint_pre,omitempty"`
	FingerprintPost string  `yaml:"fingerprint_post,omitempty" json:"fingerprint_post,omitempty"`
	// SkippedReason carries the dependency-cascade explanation when
	// Verdict == VerdictSkipped. Format: "dep-unmet: <upstream-id>".
	SkippedReason string `yaml:"skipped_reason,omitempty" json:"skipped_reason,omitempty"`
}

// FinalReport is the aggregate persisted to result-{calver}.yml.
type FinalReport struct {
	Schema              int               `yaml:"schema" json:"schema"`
	Score               string            `yaml:"score" json:"score"`
	Calver              string            `yaml:"calver" json:"calver"`
	RunID               string            `yaml:"run_id" json:"run_id"`
	Agent               string            `yaml:"agent" json:"agent"`
	AgentVersion        map[string]string `yaml:"agent_version,omitempty" json:"agent_version,omitempty"`
	Where               ReportWhere       `yaml:"where" json:"where"`
	TargetImage         string            `yaml:"target_image,omitempty" json:"target_image,omitempty"`
	Tag                 string            `yaml:"tag,omitempty" json:"tag,omitempty"`
	PlateauIteration    int               `yaml:"plateau_iteration" json:"plateau_iteration"`
	MCPEndpoint         string            `yaml:"mcp_endpoint,omitempty" json:"mcp_endpoint,omitempty"`
	StartedUTC          string            `yaml:"started_utc" json:"started_utc"`
	FinishedUTC         string            `yaml:"finished_utc" json:"finished_utc"`
	ExitReason          string            `yaml:"exit_reason" json:"exit_reason"` // plateau | solved-all | interrupted | dry-run
	IterationsRun       int               `yaml:"iterations_run" json:"iterations_run"`
	BestScore           int               `yaml:"best_score" json:"best_score"`
	BestIteration       int               `yaml:"best_iteration" json:"best_iteration"`
	CharlyharnessBranch string            `yaml:"ovharness_branch,omitempty" json:"ovharness_branch,omitempty"`
	Summary             ReportSummary     `yaml:"summary" json:"summary"`
	Phases              []PhaseReport     `yaml:"phase,omitempty" json:"phase,omitempty"`
	PhasesCompleted     int               `yaml:"phases_completed,omitempty" json:"phases_completed,omitempty"`
	Iterations          []IterationState  `yaml:"iteration,omitempty" json:"iteration,omitempty"`
	FinalStep           []StepVerdict     `yaml:"final_step,omitempty" json:"final_step,omitempty"`
}

// PhaseReport summarizes one phase of a progressive run.
type PhaseReport struct {
	N             int    `yaml:"n" json:"n"`
	IterationsRun int    `yaml:"iterations_run" json:"iterations_run"`
	ExitReason    string `yaml:"exit_reason" json:"exit_reason"` // solved-all | plateau | interrupted
	Score         int    `yaml:"score" json:"score"`
	Total         int    `yaml:"total" json:"total"`
}

// ReportWhere identifies the target a run executed against.
type ReportWhere struct {
	Kind string `yaml:"kind" json:"kind"`                     // pod | vm | host
	Name string `yaml:"name,omitempty" json:"name,omitempty"` // pod or vm name; absent for host
}

// ReportSummary is the aggregate metrics panel.
type ReportSummary struct {
	Input         int     `yaml:"input" json:"input"`
	Solved        int     `yaml:"solved" json:"solved"`
	Partial       int     `yaml:"partial" json:"partial"`
	Unchanged     int     `yaml:"unchanged" json:"unchanged"`
	Regressed     int     `yaml:"regressed" json:"regressed"`
	Tampered      int     `yaml:"tampered" json:"tampered"`
	Added         int     `yaml:"added" json:"added"`
	Skipped       int     `yaml:"skipped,omitempty" json:"skipped,omitempty"`
	PercentSolved float64 `yaml:"percent_solved" json:"percent_solved"`
}

// ---------------------------------------------------------------------------
// Subprocess seams (test hooks)
// ---------------------------------------------------------------------------

// findCharlyForCheck returns the path to the charly binary the harness
// should re-invoke for sub-commands. Prefers os.Executable() so the
// harness's own build is used.
func findCharlyForCheck() string {
	if exe, err := os.Executable(); err == nil && exe != "" {
		return exe
	}
	return "charly"
}

// buildImageFn builds the target image from the per-run repo into tag.
var buildImageFn = func(ctx context.Context, repoDir, image, tag, logPath string) (time.Duration, error) {
	start := time.Now()
	cmd := exec.CommandContext(ctx, findCharlyForCheck(), "-C", repoDir,
		"box", "build", image, "--tag", tag)
	if logPath != "" {
		f, err := os.Create(logPath)
		if err == nil {
			cmd.Stdout = f
			cmd.Stderr = f
			defer f.Close() //nolint:errcheck
		}
	}
	err := cmd.Run()
	return time.Since(start), err
}

// runCharlyImageTestFn shells out to `charly check box <tag> --format yaml`.
var runCharlyImageTestFn = func(ctx context.Context, tag string) ([]byte, time.Duration, error) {
	start := time.Now()
	cmd := exec.CommandContext(ctx, findCharlyForCheck(), "check", "box", tag, "--format", "yaml")
	out, err := cmd.Output()
	return out, time.Since(start), err
}

// RunnerStreamConfig customizes runRunnerFn's stdout/stderr handling
// for AIs that emit structured output. When OutputFormat is empty, the
// runner uses the legacy merged-stream path (stdout+stderr → logPath).
// When OutputFormat is "stream-json", stdout is teed to NdjsonPath
// AND parsed line-by-line into RunnerEvents dispatched to OnEvent;
// stderr is written to StderrPath.
//
// The merged-stream path is preserved verbatim for codex / gemini and
// any AI without explicit stream-json support — switching the AI's
// output_format flips the entire stdout pipeline atomically.
type RunnerStreamConfig struct {
	OutputFormat string            // "" | "stream-json"
	NdjsonPath   string            // stream-json only — raw NDJSON tee
	StderrPath   string            // stream-json only — separate stderr file
	OnEvent      func(RunnerEvent) // stream-json only — called per parsed line
}

// runRunnerFn invokes the runner inside the active target. When
// stream is non-nil and stream.OutputFormat == "stream-json", stdout
// is streamed through a streamJSONSink (tee + parse) and stderr is
// written to stream.StderrPath. Otherwise stdout+stderr merge into
// logPath as before.
var runRunnerFn = func(ctx context.Context, layout RunLayout, argv []string, env map[string]string, logPath string, stream *RunnerStreamConfig) (time.Duration, error) {
	start := time.Now()
	if len(argv) == 0 {
		return 0, fmt.Errorf("harness: runner has empty command")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = layout.RepoDir
	cmd.Env = mergeOsEnv(env)

	if stream != nil && stream.OutputFormat == AgentOutputFormatStreamJSON {
		sink, err := newStreamJSONSink(stream.NdjsonPath, stream.OnEvent)
		if err != nil {
			return 0, fmt.Errorf("harness: open ndjson sink: %w", err)
		}
		defer sink.Close() //nolint:errcheck
		stderrFile, err := os.Create(stream.StderrPath)
		if err != nil {
			return 0, fmt.Errorf("harness: open stderr file: %w", err)
		}
		defer stderrFile.Close() //nolint:errcheck
		cmd.Stdout = sink
		cmd.Stderr = stderrFile
		runErr := cmd.Run()
		return time.Since(start), runErr
	}

	if logPath != "" {
		f, ferr := os.Create(logPath)
		if ferr == nil {
			cmd.Stdout = f
			cmd.Stderr = f
			defer f.Close() //nolint:errcheck
		}
	}
	err := cmd.Run()
	return time.Since(start), err
}

// mergeOsEnv returns os.Environ() merged with overrides from env.
func mergeOsEnv(env map[string]string) []string {
	if len(env) == 0 {
		return os.Environ()
	}
	out := append([]string(nil), os.Environ()...)
	for k, v := range env {
		prefix := k + "="
		replaced := false
		for i, e := range out {
			if strings.HasPrefix(e, prefix) {
				out[i] = prefix + v
				replaced = true
				break
			}
		}
		if !replaced {
			out = append(out, prefix+v)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// RunHarness — the main entry point
// ---------------------------------------------------------------------------

// RunHarness executes the iteration loop against opts and returns
// the final report. Caller is responsible for creating the per-run
// clone and for collecting the pre-AI baseline (those happen in
// CheckRunLocalCmd.Run, inside the target).
//
// Loop bounds, post-cutover:
//   - solved-all: every scored step has been solved
//   - plateau: plateau_counter >= plateau_iteration
//   - dry-run: after iter 1 with --dry-run
//   - interrupted: ctx cancelled
func RunHarness(ctx context.Context, opts HarnessOpts, layout RunLayout) (*FinalReport, error) {
	started := time.Now().UTC()
	report := &FinalReport{
		Schema:              1,
		Score:               opts.ScoreName,
		Calver:              ComputeCalVer(),
		RunID:               layout.RunID,
		Agent:               opts.AgentName,
		Where:               ReportWhere{Kind: opts.TargetKind, Name: opts.TargetName},
		TargetImage:         opts.TargetImage,
		Tag:                 opts.Tag,
		PlateauIteration:    opts.PlateauIteration,
		MCPEndpoint:         opts.MCPEndpoint,
		CharlyharnessBranch: layout.Branch,
		StartedUTC:          started.Format(time.RFC3339),
	}

	plateauCounter := 0
	bestScore := 0
	bestIteration := 0
	prevScore := 0
	preIDs := stepIDSet(opts.PreAIStep)

	// Iteration loop — plateau-bounded, no max-iteration ceiling.
	for k := 1; ; k++ {
		// Compute still-unsolved.
		unsolved := stillUnsolved(opts.PreAIStep, report.Iterations)
		if len(unsolved) == 0 && k > 1 {
			report.ExitReason = "solved-all"
			break
		}

		iterState, err := runOneIteration(ctx, opts, layout, k, unsolved, report, prevScore, plateauCounter, started)
		if err != nil {
			return report, fmt.Errorf("iter%d: %w", k, err)
		}
		report.Iterations = append(report.Iterations, iterState)

		// Dry-run exits after iter 1 writes its scope+prompt.
		if opts.DryRun {
			report.ExitReason = "dry-run"
			break
		}

		// Compute delta vs previous iter (k==1 → prevScore=0 so delta=Score).
		iterState.ScoreDelta = iterState.Score - prevScore
		report.Iterations[len(report.Iterations)-1].ScoreDelta = iterState.ScoreDelta

		// Plateau bookkeeping.
		if iterState.Score > bestScore {
			bestScore = iterState.Score
			bestIteration = k
			plateauCounter = 0
		} else {
			plateauCounter++
		}
		report.Iterations[len(report.Iterations)-1].PlateauCounterAfter = plateauCounter
		_ = writeIterScore(layout, k, report.Iterations[len(report.Iterations)-1])

		prevScore = iterState.Score

		// Plateau exit (only loop bound besides solved-all/dry-run/ctx).
		if opts.PlateauIteration > 0 && plateauCounter >= opts.PlateauIteration {
			report.ExitReason = "plateau"
			break
		}

		// Ctx cancellation.
		if ctx.Err() != nil {
			report.ExitReason = "interrupted"
			break
		}
	}

	finished := time.Now().UTC()
	report.FinishedUTC = finished.Format(time.RFC3339)
	report.BestScore = bestScore
	report.BestIteration = bestIteration
	report.IterationsRun = len(report.Iterations)
	if report.ExitReason == "" {
		report.ExitReason = "interrupted"
	}

	// Aggregate per-step final verdicts.
	if n := len(report.Iterations); n > 0 {
		report.FinalStep = report.Iterations[n-1].Step
	}
	report.Summary = computeSummary(report.FinalStep, len(preIDs))

	if err := writeReport(layout, report); err != nil {
		return report, fmt.Errorf("write report: %w", err)
	}
	return report, nil
}

// stepIDSet returns a set of step IDs from a frozen list.
func stepIDSet(steps []StepScore) map[string]bool {
	out := make(map[string]bool, len(steps))
	for _, s := range steps {
		out[s.ID] = true
	}
	return out
}

// stillUnsolved returns scored steps still in play across the run so far.
func stillUnsolved(pre []StepScore, iters []IterationState) []StepScore {
	latest := make(map[string]Verdict)
	for _, it := range iters {
		for _, v := range it.Step {
			latest[v.ID] = v.Verdict
		}
	}
	var out []StepScore
	for _, s := range pre {
		v, seen := latest[s.ID]
		if !seen {
			out = append(out, s)
			continue
		}
		if v == VerdictSolved || v == VerdictTampered {
			continue
		}
		out = append(out, s)
	}
	return out
}

// ---------------------------------------------------------------------------
// runOneIteration
// ---------------------------------------------------------------------------

// runOneIteration drives a single pass through the iteration body.
func runOneIteration(
	ctx context.Context,
	opts HarnessOpts,
	layout RunLayout,
	k int,
	unsolved []StepScore,
	reportSoFar *FinalReport,
	prevScore int,
	plateauCounterEntering int,
	benchmarkStart time.Time,
) (iter IterationState, err error) {
	iter = IterationState{K: k, Phase: opts.Phase}
	iterStart := time.Now().UTC()
	iter.StartedUTC = iterStart.Format(time.RFC3339)
	// Stamp FinishedUTC + IterationDuration on every return path —
	// success and failure alike. omitempty means failed-early returns
	// (mkdir / scope-write / prompt-write) still produce sensible
	// records. Named return values make this a single closure rather
	// than five copies sprinkled through the function body.
	defer func() {
		finished := time.Now().UTC()
		iter.FinishedUTC = finished.Format(time.RFC3339)
		iter.IterationDuration = finished.Sub(iterStart).String()
	}()
	// iterMu serializes appends to iter.RunnerEvent (from the parser
	// goroutine in stream-json mode) and iter.WatchdogSample (from the
	// watchdog goroutine). Both writers use this same lock — straight
	// sync.Mutex is sufficient since this is per-iteration scope.
	var iterMu sync.Mutex
	iterDir := layout.IterDir(k)
	if err := os.MkdirAll(iterDir, 0o755); err != nil {
		return iter, fmt.Errorf("mkdir iter%d: %w", k, err)
	}

	// 0. Pre-iter fixture-persistence check (iter ≥ 2 only): probe whether
	// every in-scope step's `pod:` is still running inside the harness sandbox.
	// Per the harness contract, fixtures from earlier phases must persist
	// for cumulative scoring; if one disappeared (R10 saw charly-desktop's
	// supervisord exit cleanly mid-run between phases 6 and 7), warn the
	// AI via stderr — its prompt context will pick up the warning. Don't
	// auto-redeploy: that's the AI's job. Skip on iter1 (no prior fixtures
	// expected yet — they're being deployed in this iter for the first time).
	if k > 1 {
		warnMissingInScopePods(opts.MergedPlan)
	}

	// 1. Write scope.yml
	scope := renderScope(opts, layout, k, reportSoFar, unsolved)
	if err := writeScope(layout, k, scope); err != nil {
		return iter, fmt.Errorf("write scope: %w", err)
	}

	// 2. Render + write prompt.md
	notesSnap := opts.Notes
	if opts.Iterate != nil && opts.Iterate.NotesEnabled() {
		runNotesPath := NotePathForRun(layout.HarnessRoot, layout.RunID)
		if data, err := os.ReadFile(runNotesPath); err == nil {
			notesSnap = string(data)
		} else {
			notesSnap = ""
		}
	}
	mcp := opts.MCPEndpoint
	if mcp == "" {
		mcp = DefaultMCPEndpoint
	}
	// ${PLAN} = the full scored plan; ${CHECKS} = the still-unsolved check
	// subset (rendered from the unsolved StepScore ids against the plan).
	planYAML := RenderPlanYAML(opts.MergedPlan)
	checksYAML := RenderPlanYAML(unsolvedPlanSubset(opts.MergedPlan, unsolved))
	deploymentName := opts.Deploy
	scoreDelta := 0
	if k > 1 {
		scoreDelta = priorScore(reportSoFar) - prevScore
		// Note: at this point reportSoFar.Iterations[-1] doesn't yet
		// exist for iter k. The "delta last-shown to AI" is
		// (priorIter.Score - prevPrevScore). For k==1 delta is 0.
		// Simpler model: pass scoreDelta as the previous iter's delta.
		if n := len(reportSoFar.Iterations); n > 0 {
			scoreDelta = reportSoFar.Iterations[n-1].ScoreDelta
		}
	}
	attemptsLeft := max(opts.PlateauIteration-plateauCounterEntering, 0)
	phaseIntro := ""
	substCtx := &SubstContext{
		RunID:            layout.RunID,
		ScoreName:        opts.ScoreName,
		AgentName:        opts.AgentName,
		WorkspacePath:    layout.RepoDir,
		TargetImage:      opts.TargetImage,
		TargetKind:       opts.TargetKind,
		TargetName:       opts.TargetName,
		Iteration:        k,
		PlateauIteration: opts.PlateauIteration,
		PlateauCounter:   plateauCounterEntering,
		BestScore:        reportSoFar.BestScore,
		ScoreDelta:       scoreDelta,
		AttemptsLeft:     attemptsLeft,
		MCPEndpoint:      mcp,
		Notes:            notesSnap,
		Plan:             planYAML,
		Checks:           checksYAML,
		Phase:            opts.Phase,
		PhaseTotal:       opts.PhaseTotal,
		PhaseIntro:       phaseIntro,
		Deploy:           deploymentName,
		Tag:              opts.Tag,
		Timeout:          opts.Agent.Timeout,
	}
	if opts.Iterate != nil {
		substCtx.AppendEnv(opts.Iterate.Env)
	}
	if opts.Agent != nil {
		substCtx.AppendEnv(opts.Agent.Env)
	}
	promptText := Substitute(opts.Prompt, substCtx)
	if err := writePrompt(layout, k, promptText); err != nil {
		return iter, fmt.Errorf("write prompt: %w", err)
	}

	// Dry-run exits here without invoking the runner.
	if opts.DryRun {
		return iter, nil
	}

	return dispatchRunnerAndScore(ctx, opts, layout, k, iterDir, substCtx, promptText, reportSoFar, benchmarkStart, iter, &iterMu)
}

// dispatchRunnerAndScore invokes the AI runner under an optional
// per-iteration timeout + score-progress watchdog, then scores the result
// (live-plan or image-test path) and classifies every step into the
// iteration record. Split out of runOneIteration, which keeps the
// pre-runner scope/prompt setup inline and hands the partially-built
// IterationState here for completion.
//
//nolint:gocyclo // cohesive sequential pipeline (runner dispatch → watchdog → score → step classification) already split out of runOneIteration; further extraction would over-fragment the scoring flow
func dispatchRunnerAndScore(
	ctx context.Context,
	opts HarnessOpts,
	layout RunLayout,
	k int,
	iterDir string,
	substCtx *SubstContext,
	promptText string,
	reportSoFar *FinalReport,
	benchmarkStart time.Time,
	iter IterationState,
	iterMu *sync.Mutex,
) (IterationState, error) {
	// 3. Dispatch the runner.
	runnerArgv, runnerEnv := renderRunnerInvocation(opts, substCtx, promptText, iterDir)
	runnerLog := filepath.Join(iterDir, "runner.log")

	// Per-iteration wall-clock cap is OPT-IN via `ai.<name>.timeout:`.
	// Empty (the default) → no cap; the runner inherits the parent
	// context's cancellation only and runs until the AI exits or the
	// user interrupts. The plateau counter is the loop bound, not wall
	// clock. The score's prompt promises "Take all the time you need" —
	// honoring that promise is the harness's job.
	timeout, _ := ParseAgentTimeout(opts.Agent.Timeout)
	var runnerCtx context.Context
	var cancelRunner context.CancelFunc
	if timeout > 0 {
		runnerCtx, cancelRunner = context.WithTimeout(ctx, timeout)
	} else {
		runnerCtx, cancelRunner = context.WithCancel(ctx)
	}

	// Score-progress watchdog. Hidden from the AI by construction —
	// runs in this Go process, never appears in the AI's prompt or any
	// tool the AI invokes. Probes the live deployments via
	// RunCheckLive every CheckInterval; reports the current
	// score to host stderr; terminates the runner if the score has not
	// improved in NoImprovementTimeout. Plateau detection (across
	// iterations) and this watchdog (within an iteration) are
	// orthogonal — both bound the run, neither penalizes legitimately
	// long iterations that ARE making progress.
	//
	// Watchdog only applies when ScoringPlan is non-empty (live-plan
	// scoring). Image-test mode runs scoring after the runner exits,
	// so there's no live-score signal to poll.
	watchdogStarted := false
	var watchdogDone chan struct{}
	if len(opts.ScoringPlan) > 0 {
		checkInterval, _ := ParseAgentTimeout(opts.Agent.ProgressCheckInterval)
		if checkInterval == 0 {
			checkInterval = DefaultProgressCheckInterval
		}
		noImpTimeout, _ := ParseAgentTimeout(opts.Agent.ProgressNoImprovementTimeout)
		if noImpTimeout == 0 {
			noImpTimeout = DefaultProgressNoImprovementTimeout
		}
		if checkInterval > 0 {
			scoringPlan := opts.ScoringPlan
			deployment := opts.Deploy
			scoreName := opts.ScoreName
			phase, phaseTotal, iterK := opts.Phase, opts.PhaseTotal, k
			stderr := opts.Stderr
			wd := &ProgressWatchdog{
				CheckInterval:        checkInterval,
				NoImprovementTimeout: noImpTimeout,
				BenchmarkStart:       benchmarkStart,
				Probe: func(probeCtx context.Context) (int, int, error) {
					live, err := RunCheckLive(probeCtx, deployment, scoreName, scoringPlan)
					if err != nil {
						return 0, 0, err
					}
					return live.Summary.Pass, live.Summary.Total, nil
				},
				OnTick: func(elapsed time.Duration, score, total int, lastImprovedAt time.Time) {
					// All user-facing timestamps render as offsets from the
					// benchmark's run-start (`+Nm0s into the run`) instead
					// of wall-clock HH:MM:SS — operators read run-relative
					// times far more easily than absolute clock times when
					// reasoning about plateau windows + watchdog timeouts.
					// `elapsed` here is RUN-elapsed (since RunHarness's
					// `started`), not iter-elapsed, so the operator sees a
					// monotonic offset that grows across phases. Idle (time
					// since last improvement) is an additional duration
					// because that's what predicts the no-improvement
					// watchdog firing.
					runElapsed := time.Since(benchmarkStart).Round(time.Second)
					var deltaInfo string
					if !lastImprovedAt.IsZero() {
						idle := time.Since(lastImprovedAt).Round(time.Second)
						lastImprovedRunOffset := lastImprovedAt.Sub(benchmarkStart).Round(time.Second)
						deltaInfo = fmt.Sprintf(" (last improvement %s ago, at +%s into the run)",
							idle, lastImprovedRunOffset)
					} else {
						deltaInfo = " (no improvement observed yet)"
					}
					_ = elapsed // kept for callback signature stability; runElapsed is canonical
					fmt.Fprintf(stderr,
						"harness: progress [phase %d/%d iter %d] +%s into the run — current score %d/%d%s\n",
						phase, phaseTotal, iterK, runElapsed, score, total, deltaInfo)
					// Persist the same observation into the iteration
					// record so result-{calver}.yml carries the score
					// timeline as a structured field (not just an
					// ephemeral stderr line).
					sample := WatchdogSample{
						AtUTC:   time.Now().UTC().Format(time.RFC3339),
						Elapsed: elapsed.Round(time.Second).String(),
						Score:   score,
						Total:   total,
					}
					if !lastImprovedAt.IsZero() {
						sample.LastImprovedAt = lastImprovedAt.UTC().Format(time.RFC3339)
					}
					iterMu.Lock()
					iter.WatchdogSample = append(iter.WatchdogSample, sample)
					iterMu.Unlock()
					// Also append the sample as a JSON line to a host-
					// visible <iter-dir>/watchdog.jsonl file so operators
					// can `tail -f` mid-iteration. The result.yml carries
					// the same timeline as a structured field, but it is
					// only flushed at iter end — the JSONL stream is the
					// only live observation surface. Best-effort: write
					// failures are logged but don't disrupt the watchdog
					// or the AI runner.
					if data, err := json.Marshal(sample); err == nil {
						path := filepath.Join(layout.IterDir(iterK), "watchdog.jsonl")
						if f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
							_, _ = f.Write(append(data, '\n'))
							_ = f.Close()
						}
					}
				},
				OnTickError: func(err error) {
					fmt.Fprintf(stderr,
						"harness: progress [phase %d/%d iter %d] probe error (continuing): %v\n",
						phase, phaseTotal, iterK, err)
				},
				OnTimeout: func(reason string) {
					fmt.Fprintf(stderr,
						"harness: watchdog [phase %d/%d iter %d] terminating AI runner — %s\n",
						phase, phaseTotal, iterK, reason)
					cancelRunner()
				},
			}
			watchdogDone = make(chan struct{})
			watchdogStarted = true
			go func() {
				wd.Run(runnerCtx)
				close(watchdogDone)
			}()
		}
	}

	// Capture the post-substitution argv so result-{calver}.yml shows
	// what was actually exec'd (with ${PROMPT} expanded to the rendered
	// prompt text). Useful for replaying a problem run by hand.
	iter.RunnerCommand = append([]string(nil), runnerArgv...)

	// Build the runner stream configuration. For AIs with
	// output_format: stream-json, stdout (NDJSON) is parsed into
	// RunnerEvents and stderr is split into a sibling file. For all
	// other AIs, stream is nil → legacy merged-stream path.
	var streamCfg *RunnerStreamConfig
	if opts.Agent != nil && opts.Agent.OutputFormat == AgentOutputFormatStreamJSON {
		ndjsonPath := filepath.Join(iterDir, "runner.ndjson")
		stderrPath := filepath.Join(iterDir, "runner.stderr.log")
		streamCfg = &RunnerStreamConfig{
			OutputFormat: AgentOutputFormatStreamJSON,
			NdjsonPath:   ndjsonPath,
			StderrPath:   stderrPath,
			OnEvent: func(ev RunnerEvent) {
				iterMu.Lock()
				iter.RunnerEvent = append(iter.RunnerEvent, ev)
				iterMu.Unlock()
			},
		}
		iter.RunnerNdjsonPath = ndjsonPath
		iter.RunnerStderrPath = stderrPath
	}

	runnerDur, runnerErr := runRunnerFn(runnerCtx, layout, runnerArgv, runnerEnv, runnerLog, streamCfg)
	cancelRunner()
	if watchdogStarted {
		<-watchdogDone // ensure watchdog goroutine exits before iter completes
	}
	iter.RunnerDuration = runnerDur.String()
	if streamCfg == nil {
		// Plain runners merge stdout+stderr into runner.log; inline it
		// into the result for backward compatibility.
		iter.RunnerLogPath = runnerLog
		if data, err := os.ReadFile(runnerLog); err == nil {
			iter.RunnerOutput = string(data)
		}
	}
	if runnerErr != nil {
		fmt.Fprintf(opts.Stderr, "iter%d: runner exited with error: %v (continuing)\n", k, runnerErr)
	}

	// 4. Score against the substituted plan. The AI saw the MergedPlan slice
	// (with ${EVAL_NONCE_*} placeholders); scoring runs against ScoringPlan
	// with substituted values.
	useLivePlan := len(opts.ScoringPlan) > 0
	iterTagSuffix := fmt.Sprintf("charlycheck-%s-iter%d", layout.RunID, k)
	iterRef := fmt.Sprintf("ghcr.io/opencharly/%s:%s", opts.TargetImage, iterTagSuffix)
	var (
		testOut             []byte
		parsed              *CheckRunResults
		postFingerprints    map[string]string
		postTagFingerprints map[string]string
	)

	if useLivePlan {
		testStart := time.Now()
		live, scoreErr := RunCheckLive(ctx, opts.Deploy, opts.ScoreName, opts.ScoringPlan)
		iter.TestDuration = time.Since(testStart).String()
		if scoreErr != nil {
			iter.BuildFailure = true
			iter.Score = priorScore(reportSoFar)
			iter.Step = priorStepVerdicts(reportSoFar)
			fmt.Fprintf(opts.Stderr, "iter%d: live score: %v\n", k, scoreErr)
			if err := commitIterationBestEffort(ctx, layout, k, iter, opts); err != nil {
				fmt.Fprintf(opts.Stderr, "iter%d: commit: %v\n", k, err)
			}
			_ = writeIterScore(layout, k, iter)
			return iter, nil
		}
		parsed = live
		if data, err := yaml.Marshal(live); err == nil {
			testOut = data
			_ = os.WriteFile(filepath.Join(iterDir, "test-output.yaml"), testOut, 0o644)
		}
		postFingerprints = opts.PreFingerprints
		postTagFingerprints = opts.PreTagFingerprints
	} else if !opts.SkipRebuild {
		buildLog := filepath.Join(iterDir, "build.log")
		buildDur, buildErr := buildImageFn(ctx, layout.RepoDir, opts.TargetImage, iterTagSuffix, buildLog)
		iter.BuildDuration = buildDur.String()
		iter.BuildLogPath = buildLog
		if buildErr != nil {
			iter.BuildFailure = true
			iter.Score = priorScore(reportSoFar)
			iter.Step = priorStepVerdicts(reportSoFar)
			if err := commitIterationBestEffort(ctx, layout, k, iter, opts); err != nil {
				fmt.Fprintf(opts.Stderr, "iter%d: commit: %v\n", k, err)
			}
			_ = writeIterScore(layout, k, iter)
			return iter, nil
		}

		testStart := time.Now()
		out, _, testErr := runCharlyImageTestFn(ctx, iterRef)
		iter.TestDuration = time.Since(testStart).String()
		if testErr != nil {
			iter.BuildFailure = true
			iter.Score = priorScore(reportSoFar)
			iter.Step = priorStepVerdicts(reportSoFar)
			fmt.Fprintf(opts.Stderr, "iter%d: charly check box: %v\n", k, testErr)
			if err := commitIterationBestEffort(ctx, layout, k, iter, opts); err != nil {
				fmt.Fprintf(opts.Stderr, "iter%d: commit: %v\n", k, err)
			}
			_ = writeIterScore(layout, k, iter)
			return iter, nil
		}
		testOut = out
		_ = os.WriteFile(filepath.Join(iterDir, "test-output.yaml"), out, 0o644)
		postSet := loadDescriptionsFromDir(layout.RepoDir, opts.TargetImage)
		postFingerprints = FingerprintSet(postSet)
		postTagFingerprints = collectTagFingerprints(postSet)
	}

	if parsed == nil {
		p, err := ParseCharlyTestOutput(testOut)
		if err != nil {
			return iter, fmt.Errorf("parse test output: %w", err)
		}
		parsed = p
	}
	if postFingerprints == nil {
		postFingerprints = map[string]string{}
	}
	if postTagFingerprints == nil {
		postTagFingerprints = map[string]string{}
	}

	postByID := parsed.StepByID()
	for _, pre := range opts.PreAIStep {
		preState := StepState{
			Present:        true,
			Fingerprint:    opts.PreFingerprints[pre.ID],
			Status:         pre.Status,
			TagFingerprint: opts.PreTagFingerprints[pre.ID],
		}
		var postState StepState
		if post, ok := postByID[pre.ID]; ok {
			postState = StepState{
				Present:        true,
				Fingerprint:    postFingerprints[pre.ID],
				Status:         post.Status,
				TagFingerprint: postTagFingerprints[pre.ID],
			}
		}
		v := Classify(preState, postState)
		iter.Step = append(iter.Step, StepVerdict{
			ID:              pre.ID,
			Origin:          pre.Origin,
			Verdict:         v,
			Baseline:        pre.Status,
			Final:           postState.Status,
			FingerprintPre:  preState.Fingerprint,
			FingerprintPost: postState.Fingerprint,
		})
		if v == VerdictSolved {
			iter.Score++
		}
	}

	preIDs := stepIDSet(opts.PreAIStep)
	for id := range postByID {
		if !preIDs[id] {
			iter.AddedStep = append(iter.AddedStep, id)
		}
	}

	solvedIDs := collectSolvedIDs(iter.Step)
	if err := commitIterationBestEffort(ctx, layout, k, iter, opts); err != nil {
		fmt.Fprintf(opts.Stderr, "iter%d: commit: %v\n", k, err)
	}
	_ = solvedIDs

	if err := writeIterScore(layout, k, iter); err != nil {
		return iter, fmt.Errorf("write iter score: %w", err)
	}
	return iter, nil
}

// commitIterationBestEffort commits the iteration in the per-run clone.
//
// Before committing, emits a per-iter delta summary line and kills
// orphaned `while true; do sleep N; done` / `pgrep -f` self-match
// poll-loop bash subprocesses left dangling by the AI's
// `Bash{run_in_background: true}` + `TaskOutput`-timeout pattern
// (Claude Code issue 52328 — see
// `.check/ISSUE-claude-code-bash-pgrep-self-match-deadlock.md`). Without
// this kill, orphans accumulate across iterations, eventually wedging
// the next claude spawn (the parent claude process waits for all
// background bash subprocesses to exit before terminating itself).
//
// The kill targets two patterns observed in the 2026-04-28 R10 round:
//   - `bash -c 'while true; do sleep N; done'` (heartbeat keepalives)
//   - `bash -c '... pgrep -f "<arbitrary>" ... ; do sleep N'` (self-match polls)
//
// Best-effort: pkill failure is logged but doesn't abort the commit.
func commitIterationBestEffort(ctx context.Context, layout RunLayout, k int, iter IterationState, opts HarnessOpts) error {
	emitIterEndSummary(k, iter)
	killOrphanLoopBashes(opts.TargetKind, opts.TargetName)
	solved := collectSolvedIDs(iter.Step)
	sha, err := CommitIterationInRepo(ctx, layout, k, iter.Score, solved)
	if err != nil {
		return err
	}
	iter.CommitSHA = sha
	return nil
}

// emitIterEndSummary prints one stderr line at the end of every
// iteration with a per-iter delta breakdown: solved count this iter,
// list of failed steps (capped at 5), cascade-skipped count, and
// the new cumulative score. The watchdog's per-tick "current score"
// log shows running totals but does not delta-summarize. This is the
// "what did this iter actually change?" view operators want.
//
// Format:
//
//	harness: phase 6 iter 1 → solved 6 of 13 this iter (failed:
//	desktop-cdp-loads-web; cascade-skipped: 6 dependents); cumulative 67/74
//
// `solved this iter` counts steps with VerdictSolved (baseline
// fail → final pass) — NOT cumulative passing steps from prior
// iters. Cumulative score = total steps with `final: pass` across
// the whole scored plan.
func emitIterEndSummary(k int, iter IterationState) {
	var solvedThisIter, failedFinal, skippedFinal, cumulativePass, total int
	var failedNames []string
	for _, s := range iter.Step {
		total++
		switch s.Verdict {
		case VerdictSolved:
			solvedThisIter++
			cumulativePass++
		case VerdictUnchanged:
			// baseline pass → final pass: counts toward cumulative but
			// not solved-this-iter.
			if s.Final == "pass" {
				cumulativePass++
			} else {
				failedFinal++
				if len(failedNames) < 5 {
					failedNames = append(failedNames, stepShortName(s.ID))
				}
			}
		case VerdictSkipped:
			skippedFinal++
		case VerdictTampered:
			// counted as cumulative pass because the baseline was passing.
			// Tampering means baseline-pass + final-fail.
			failedFinal++
			if len(failedNames) < 5 {
				failedNames = append(failedNames, stepShortName(s.ID))
			}
		default:
			if s.Final == "pass" {
				cumulativePass++
			}
		}
	}
	failPart := ""
	if failedFinal > 0 {
		more := ""
		if failedFinal > len(failedNames) {
			more = fmt.Sprintf(", +%d more", failedFinal-len(failedNames))
		}
		failPart = fmt.Sprintf(" (failed: %s%s; cascade-skipped: %d)", strings.Join(failedNames, ", "), more, skippedFinal)
	} else if skippedFinal > 0 {
		failPart = fmt.Sprintf(" (cascade-skipped: %d)", skippedFinal)
	}
	fmt.Fprintf(os.Stderr,
		"harness: iter %d end → solved %d this iter%s; cumulative %d/%d\n",
		k, solvedThisIter, failPart, cumulativePass, total)
}

// stepShortName returns the tail segment of a step id, falling back to the
// full id. Used by emitIterEndSummary to keep the failed-name list compact.
func stepShortName(id string) string {
	parts := strings.Split(id, ":")
	if len(parts) >= 2 {
		return parts[len(parts)-2] + ":" + parts[len(parts)-1]
	}
	return id
}

// warnMissingInScopePods probes the running container set and warns once per
// missing fixture pod referenced by the in-scope plan steps' Op.Venue. The
// harness contract requires earlier-phase fixture pods to persist for
// cumulative scoring; this is a soft signal to the AI. Best-effort.
func warnMissingInScopePods(plan []Step) {
	uniquePods := map[string]bool{}
	for _, s := range plan {
		if s.Venue != "" {
			rootPod := s.Venue
			if i := strings.IndexByte(rootPod, '.'); i > 0 {
				rootPod = rootPod[:i]
			}
			uniquePods[rootPod] = true
		}
	}
	if len(uniquePods) == 0 {
		return
	}
	// Probe each: `podman ps --filter name=charly-<pod> --format {{.Names}}`.
	// One missing pod produces one warn line; multiple missing produce one
	// warn line each so the operator/AI sees them all.
	missing := 0
	for pod := range uniquePods {
		expected := "charly-" + strings.ReplaceAll(pod, ".", "_")
		out, err := exec.Command("podman", "ps", "--filter", "name="+expected,
			"--filter", "status=running", "--format", "{{.Names}}").Output()
		if err != nil {
			continue // probe error → silent (don't spam)
		}
		if !strings.Contains(string(out), expected) {
			fmt.Fprintf(os.Stderr,
				"harness: WARNING: in-scope fixture pod %q is not running — "+
					"earlier-phase steps that probe it will fail at iter end "+
					"unless the AI redeploys it this iteration.\n", expected)
			missing++
		}
	}
	if missing > 0 {
		fmt.Fprintf(os.Stderr,
			"harness: %d fixture pod(s) missing — see warnings above; "+
				"the AI's iteration prompt should restore them per harness contract.\n",
			missing)
	}
}

// killOrphanLoopBashes kills issue-52328 deadlock orphans inside the
// target's PID namespace. The orchestrator runs HOST-side; orphans
// accumulate INSIDE the harness sandbox (where the AI runner spawns claude,
// which forks `bash -c 'while true; do sleep N; done'` heartbeats and
// `bash -c '... pgrep -f ...; do sleep N'` self-match polls). Without
// `podman exec`, pkill on the host would scan the wrong PID namespace.
//
// Two patterns observed in the 2026-04-28 R10 round:
//   - `while true.*sleep [0-9]+`            (heartbeat keepalives)
//   - `bash -c .*pgrep -f .*sleep`          (self-match polls)
//
// Best-effort: failures are silent (no harness sandbox = nothing to kill).
// Pod-target only: vm/host targets don't have the same PID-namespace
// shape and the AI runs natively, so the issue does not apply.
func killOrphanLoopBashes(targetKind, targetName string) {
	if targetKind != "pod" || targetName == "" {
		return
	}
	container := "charly-" + targetName
	patterns := map[string]string{
		"while-true-sleep": `while true.*sleep [0-9]+`,
		"pgrep-self-match": `bash -c .*pgrep -f .*sleep`,
	}
	for label, pat := range patterns {
		// pkill -c reports kill count; -f matches full cmdline.
		// `podman exec` runs the kill inside the pod's PID namespace.
		cmd := exec.Command("podman", "exec", container, "pkill", "-c", "-f", pat)
		out, _ := cmd.Output()
		var n int
		_, _ = fmt.Sscanf(string(out), "%d", &n) // best-effort: parse failure leaves n=0 (no orphans counted)
		if n > 0 {
			fmt.Fprintf(os.Stderr, "harness: killed %d orphan bash poll-loop(s) [%s] inside %s before iter commit\n", n, label, container)
		}
	}
}

// collectSolvedIDs returns the step IDs with Verdict == Solved.
func collectSolvedIDs(v []StepVerdict) []string {
	var out []string
	for _, s := range v {
		if s.Verdict == VerdictSolved {
			out = append(out, s.ID)
		}
	}
	return out
}

// unsolvedPlanSubset returns the plan steps whose id is in the unsolved set,
// for the ${CHECKS} prompt token (the still-failing check subset).
func unsolvedPlanSubset(plan []Step, unsolved []StepScore) []Step {
	want := make(map[string]bool, len(unsolved))
	for _, u := range unsolved {
		want[u.ID] = true
	}
	var out []Step
	for i := range plan {
		id := EffectiveStepID(&plan[i], scoredPlanOrigin, i)
		if want[id] {
			out = append(out, plan[i])
		}
	}
	return out
}

// priorScore returns the last iteration's score or 0 for the first iteration.
func priorScore(r *FinalReport) int {
	if r == nil || len(r.Iterations) == 0 {
		return 0
	}
	return r.Iterations[len(r.Iterations)-1].Score
}

// priorStepVerdicts returns the last iteration's step-verdict slice.
func priorStepVerdicts(r *FinalReport) []StepVerdict {
	if r == nil || len(r.Iterations) == 0 {
		return nil
	}
	return r.Iterations[len(r.Iterations)-1].Step
}

// computePlateauSoFar returns the plateau counter going into iter k+1.
func computePlateauSoFar(r *FinalReport) int {
	if r == nil || len(r.Iterations) == 0 {
		return 0
	}
	return r.Iterations[len(r.Iterations)-1].PlateauCounterAfter
}

// ---------------------------------------------------------------------------
// Scope rendering
// ---------------------------------------------------------------------------

// HarnessScope is the YAML-serializable form of /workspace/.check/scope.yml.
type HarnessScope struct {
	RunID            string              `yaml:"run_id" json:"run_id"`
	Score            string              `yaml:"score,omitempty" json:"score,omitempty"`
	Agent            string              `yaml:"agent,omitempty" json:"agent,omitempty"`
	Iteration        int                 `yaml:"iteration" json:"iteration"`
	PlateauIteration int                 `yaml:"plateau_iteration" json:"plateau_iteration"`
	PlateauCounter   int                 `yaml:"plateau_counter" json:"plateau_counter"`
	AttemptsLeft     int                 `yaml:"attempts_left" json:"attempts_left"`
	BestScore        int                 `yaml:"best_score" json:"best_score"`
	ScoreDelta       int                 `yaml:"score_delta" json:"score_delta"`
	TargetImage      string              `yaml:"target_image" json:"target_image"`
	Where            ReportWhere         `yaml:"where" json:"where"`
	Tag              string              `yaml:"tag,omitempty" json:"tag,omitempty"`
	History          []ScopeHistoryEntry `yaml:"history,omitempty" json:"history,omitempty"`
	Step             []ScopeStep         `yaml:"step,omitempty" json:"step,omitempty"`
}

// ScopeHistoryEntry summarizes one past iteration for the AI.
type ScopeHistoryEntry struct {
	K                   int      `yaml:"k" json:"k"`
	Score               int      `yaml:"score" json:"score"`
	ScoreDelta          int      `yaml:"score_delta" json:"score_delta"`
	SolvedIDs           []string `yaml:"solved_id,omitempty" json:"solved_id,omitempty"`
	NewlySolvedIDs      []string `yaml:"newly_solved_id,omitempty" json:"newly_solved_id,omitempty"`
	Runtime             string   `yaml:"runtime,omitempty" json:"runtime,omitempty"`
	PlateauCounterAfter int      `yaml:"plateau_counter_after,omitempty" json:"plateau_counter_after,omitempty"`
}

// ScopeStep is one still-unsolved scored step as the AI sees it.
type ScopeStep struct {
	ID              string `yaml:"id" json:"id"`
	Origin          string `yaml:"origin,omitempty" json:"origin,omitempty"`
	BaselineVerdict string `yaml:"baseline_verdict,omitempty" json:"baseline_verdict,omitempty"`
}

// renderScope builds the Scope that iteration k will see.
func renderScope(opts HarnessOpts, layout RunLayout, k int, reportSoFar *FinalReport, unsolved []StepScore) *HarnessScope {
	plateauCounter := computePlateauSoFar(reportSoFar)
	attemptsLeft := max(opts.PlateauIteration-plateauCounter, 0)
	scoreDelta := 0
	if n := len(reportSoFar.Iterations); n > 0 {
		scoreDelta = reportSoFar.Iterations[n-1].ScoreDelta
	}
	s := &HarnessScope{
		RunID:            layout.RunID,
		Score:            opts.ScoreName,
		Agent:            opts.AgentName,
		Iteration:        k,
		PlateauIteration: opts.PlateauIteration,
		PlateauCounter:   plateauCounter,
		AttemptsLeft:     attemptsLeft,
		BestScore:        reportSoFar.BestScore,
		ScoreDelta:       scoreDelta,
		TargetImage:      opts.TargetImage,
		Where:            ReportWhere{Kind: opts.TargetKind, Name: opts.TargetName},
		Tag:              opts.Tag,
	}
	for _, h := range reportSoFar.Iterations {
		s.History = append(s.History, ScopeHistoryEntry{
			K:                   h.K,
			Score:               h.Score,
			ScoreDelta:          h.ScoreDelta,
			SolvedIDs:           collectSolvedIDs(h.Step),
			Runtime:             h.RunnerDuration,
			PlateauCounterAfter: h.PlateauCounterAfter,
		})
	}
	for _, u := range unsolved {
		s.Step = append(s.Step, ScopeStep{
			ID:              u.ID,
			Origin:          u.Origin,
			BaselineVerdict: u.Status,
		})
	}
	return s
}

// writeScope writes scope.yml to iter<k>/ AND mirrors to the per-run clone.
func writeScope(layout RunLayout, k int, s *HarnessScope) error {
	data, err := yaml.Marshal(s)
	if err != nil {
		return err
	}
	iterPath := filepath.Join(layout.IterDir(k), "scope.yml")
	if err := os.WriteFile(iterPath, data, 0o644); err != nil {
		return err
	}
	mirrorDir := filepath.Join(layout.RepoDir, ".check")
	if err := os.MkdirAll(mirrorDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(mirrorDir, "scope.yml"), data, 0o644)
}

// writePrompt mirrors prompt.md alongside scope.yml.
func writePrompt(layout RunLayout, k int, text string) error {
	iterPath := filepath.Join(layout.IterDir(k), "prompt.md")
	if err := os.WriteFile(iterPath, []byte(text), 0o644); err != nil {
		return err
	}
	mirrorDir := filepath.Join(layout.RepoDir, ".check")
	if err := os.MkdirAll(mirrorDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(mirrorDir, "prompt.md"), []byte(text), 0o644)
}

// writeIterScore writes iter<k>/score.yml.
func writeIterScore(layout RunLayout, k int, state IterationState) error {
	data, err := yaml.Marshal(state)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(layout.IterDir(k), "score.yml"), data, 0o644)
}

// writeReport writes the aggregated result-{calver}.yml.
func writeReport(layout RunLayout, r *FinalReport) error {
	if r.Calver == "" {
		r.Calver = ComputeCalVer()
	}
	if r.Score == "" {
		r.Score = layout.Score
	}
	if r.Schema == 0 {
		r.Schema = 1
	}
	data, err := yaml.Marshal(r)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(layout.ResultsDir(), 0o755); err != nil {
		return err
	}
	resultPath := filepath.Join(layout.ResultsDir(), "result-"+r.Calver+".yml")
	return os.WriteFile(resultPath, data, 0o644)
}

// printHarnessReport renders a summary of the run to stdout.
func printHarnessReport(w *os.File, r *FinalReport, format string) {
	if format == "yaml" {
		data, _ := yaml.Marshal(r)
		_, _ = w.Write(data)
		return
	}
	fmt.Fprintf(w, "harness: score=%s ai=%s exit=%s iterations=%d best=%d/%d\n",
		r.Score, r.Agent, r.ExitReason, r.IterationsRun, r.BestScore, r.Summary.Input)
	fmt.Fprintf(w, "  result: .check/%s/results/result-%s.yml\n", r.Score, r.Calver)
	fmt.Fprintf(w, "  branch: %s\n", r.CharlyharnessBranch)
}

// ---------------------------------------------------------------------------
// Runner argv + env rendering
// ---------------------------------------------------------------------------

// renderRunnerInvocation prepares the argv + env the dispatcher executes.
func renderRunnerInvocation(opts HarnessOpts, substCtx *SubstContext, promptText, iterDir string) ([]string, map[string]string) {
	if opts.Agent.PromptVia == "file" {
		path := filepath.Join(iterDir, "prompt-arg.md")
		_ = os.WriteFile(path, []byte(promptText), 0o644)
		substCtx.PromptFile = path
	}
	if opts.Agent.PromptVia == "argv" || opts.Agent.PromptVia == "" {
		substCtx.Prompt = promptText
	}

	argv := SubstituteArgv(opts.Agent.Command, substCtx)
	env := SubstituteEnv(opts.Agent.Env, substCtx)
	if env == nil {
		env = make(map[string]string)
	}
	env["CHARLY_EVAL_RUN_ID"] = substCtx.RunID
	env["CHARLY_EVAL_ITERATION"] = fmt.Sprintf("%d", substCtx.Iteration)
	env["CHARLY_EVAL_SCORE"] = substCtx.ScoreName
	env["CHARLY_EVAL_AGENT"] = substCtx.AgentName
	env["CHARLY_EVAL_TARGET_KIND"] = substCtx.TargetKind
	env["CHARLY_EVAL_TARGET_NAME"] = substCtx.TargetName
	// CHARLY_EVAL_PHASE is the 1-indexed phase number (0 when the run
	// is single-pass / non-progressive). `charly check self-evaluate`
	// uses this to resolve the in-scope steps for the current phase
	// the same way the orchestrator's scorer does.
	env["CHARLY_EVAL_PHASE"] = fmt.Sprintf("%d", substCtx.Phase)
	if opts.Iterate != nil && opts.Iterate.NotesEnabled() {
		harnessRoot := HarnessDataRoot(opts.ProjectDir, opts.ScoreName)
		env["CHARLY_EVAL_NOTES_FILE"] = NotePathForRun(harnessRoot, substCtx.RunID)
	}
	return argv, env
}

// ---------------------------------------------------------------------------
// Post-AI description reload + tag-fingerprint collection
// ---------------------------------------------------------------------------

func collectTagFingerprints(set *LabelDescriptionSet) map[string]string {
	out := make(map[string]string)
	if set == nil {
		return out
	}
	for _, sec := range [][]LabeledDescription{set.Candy, set.Box, set.Deploy} {
		for _, ld := range sec {
			for sIdx, step := range ld.Plan {
				id := EffectiveStepID(&step, ld.Origin, sIdx)
				out[id] = FingerprintTags(step.Tag)
			}
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Summary aggregation
// ---------------------------------------------------------------------------

func computeSummary(steps []StepVerdict, total int) ReportSummary {
	s := ReportSummary{Input: total}
	for _, v := range steps {
		switch v.Verdict {
		case VerdictSolved:
			s.Solved++
		case VerdictUnchanged:
			s.Unchanged++
		case VerdictRegressed:
			s.Regressed++
		case VerdictTampered:
			s.Tampered++
		case VerdictAdded:
			s.Added++
		case VerdictSkipped:
			s.Skipped++
		}
	}
	if total > 0 {
		s.PercentSolved = float64(s.Solved) / float64(total) * 100.0
	}
	return s
}

// ---------------------------------------------------------------------------
// Scope-from-env — `charly check scope` handler
// ---------------------------------------------------------------------------

// ResolveAndPrintScope reads CHARLY_EVAL_RUN_ID from the environment,
// locates the active scope.yml inside the per-run clone, and writes
// its contents to out.
func ResolveAndPrintScope(projectDir string, stdout *os.File) error {
	var candidates []string
	runID := os.Getenv("CHARLY_EVAL_RUN_ID")
	if runID != "" {
		candidates = append(candidates,
			filepath.Join("/workspace", ".harness", runID, "repo", ".harness", "scope.yml"),
			filepath.Join(projectDir, ".check", runID, "repo", ".harness", "scope.yml"),
		)
	}
	candidates = append(candidates, filepath.Join(projectDir, ".check", "scope.yml"))

	for _, p := range candidates {
		data, err := os.ReadFile(p)
		if err == nil {
			_, _ = stdout.Write(data)
			return nil
		}
	}
	return fmt.Errorf("harness scope: no scope.yml found (tried %s)", strings.Join(candidates, ", "))
}

// ResolveLastTestTag reads CHARLY_EVAL_RUN_ID + CHARLY_EVAL_ITERATION
// from the environment and prints the prior iteration's image tag.
func ResolveLastTestTag(targetImage string, stdout *os.File) error {
	runID := os.Getenv("CHARLY_EVAL_RUN_ID")
	if runID == "" {
		return fmt.Errorf("harness: CHARLY_EVAL_RUN_ID not set")
	}
	iterStr := os.Getenv("CHARLY_EVAL_ITERATION")
	var iter int
	_, _ = fmt.Sscanf(iterStr, "%d", &iter) // best-effort: parse failure leaves iter=0, caught by the iter<=1 guard
	if iter <= 1 {
		return fmt.Errorf("harness: no prior iteration on iter %d", iter)
	}
	tag := fmt.Sprintf("ghcr.io/opencharly/%s:charlycheck-%s-iter%d", targetImage, runID, iter-1)
	fmt.Fprintln(stdout, tag)
	return nil
}
