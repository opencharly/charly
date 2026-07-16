package check

// harness_loop.go — the AI iteration state machine for `charly check run` (P12:
// relocated from charly/check_loop.go).
//
// The loop is keyed on an entity's `iterate:` block (spec.Iterate) carried on a
// disposable deploy / check bed. Per iteration: the AI sees the full scored plan via
// ${PLAN} + the still-unsolved check:/agent-check: subset via ${CHECKS}; the harness
// scores the plan's check:/agent-check: STEPS (score = total solved across the whole
// plan). The only loop bound is plateau detection.
//
// Core-Mechanism coupling is routed through the host seams: live plan SCORING
// (RunCheckLive, registry/venue-coupled) rides the "score" check-run mode
// (scoreLive); the per-iteration `charly box build` / `charly check box` reentry
// rides HostBuild("cli") (buildImageFn / runCharlyImageTestFn); the AI runner exec,
// the orphan-bash defense, and the fixture-pod probe are generic OS tools run
// plugin-locally.

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

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
	"gopkg.in/yaml.v3"
)

// ---------------------------------------------------------------------------
// Opts + state types
// ---------------------------------------------------------------------------

// HarnessOpts are the resolved inputs to a run, populated by CheckRunLocalCmd.Run
// before calling RunHarness.
type HarnessOpts struct {
	ProjectDir string
	ScoreName  string // the iterate entity name (deploy / check bed)
	Iterate    *spec.Iterate

	// MergedPlan is the entity's scored plan (check:/agent-check: + agent-run: +
	// runtime-context run:), baked + include-expanded + inline, with any
	// ${EVAL_NONCE_*} placeholders UN-substituted. Drives ${PLAN}/${CHECKS} — the
	// slice the AI sees.
	MergedPlan []spec.Step

	// ScoringPlan is MergedPlan with all ${EVAL_NONCE_*} tokens substituted to
	// their per-run hex values. Drives baseline synthesis AND per-iter scoring.
	ScoringPlan []spec.Step

	TargetKind string // "pod" | "vm" | "host"
	TargetName string // pod or vm name (empty when host)
	AgentName  string

	// Phase / PhaseTotal carry progressive-scoping context (0/0 = single-pass).
	Phase            int
	PhaseTotal       int
	Agent            *spec.AgentExecSpec
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
	// ${DEPLOYMENT}). Informational for the prompt.
	Deploy string

	// PreAIStep is the frozen set of scored steps (from synthesizeScoreBaseline).
	PreAIStep []spec.StepScore

	// PreFingerprints maps step id -> body fingerprint at baseline.
	PreFingerprints map[string]string

	// PreTagFingerprints maps step id -> tag fingerprint at baseline.
	PreTagFingerprints map[string]string
}

// IterationState captures one iteration's outputs.
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
type RunnerEvent struct {
	AtUTC string         `yaml:"at_utc" json:"at_utc"`
	Type  string         `yaml:"type,omitempty" json:"type,omitempty"`
	Raw   map[string]any `yaml:"raw" json:"raw"`
}

// WatchdogSample is one tick of the score-progress watchdog (default 5m cadence).
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
	// SkippedReason carries the dependency-cascade explanation when Verdict ==
	// VerdictSkipped. Format: "dep-unmet: <upstream-id>".
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
// Seam-driven subprocess helpers
// ---------------------------------------------------------------------------

// findCharlyForCheck returns the host charly binary the harness re-invokes.
// LocalTransport stamps CHARLY_BIN with its host executable; an out-of-process
// check plugin's own executable is the provider binary, not the CLI that must
// drive the next check phase.
func findCharlyForCheck() string {
	if bin := os.Getenv("CHARLY_BIN"); bin != "" {
		return bin
	}
	if exe, err := os.Executable(); err == nil && exe != "" {
		return exe
	}
	return "charly"
}

// scoreLive walks the substituted scoring plan against the live deployments via the
// "score" check-run host seam (RunCheckLive stays a host atom; the plugin owns the
// scoring math over the returned *CheckRunResults). Uses the package cmdExec (valid
// for the whole command dispatch) + the passed ctx so a watchdog probe honours its
// own context.
func scoreLive(ctx context.Context, scoreName string, plan []spec.Step) (*spec.CheckRunResults, error) {
	if cmdExec == nil {
		return nil, fmt.Errorf("charly check: scoring requires compiled-in placement (the check-run host seam is unavailable out-of-process)")
	}
	reqJSON, err := json.Marshal(spec.CheckRunRequest{Mode: "score", Name: scoreName, Plan: plan})
	if err != nil {
		return nil, err
	}
	out, err := cmdExec.HostBuild(ctx, "check-run", reqJSON)
	if err != nil {
		return nil, err
	}
	var reply kit.CheckRunReply
	if err := json.Unmarshal(out, &reply); err != nil {
		return nil, fmt.Errorf("check-run score: decode reply: %w", err)
	}
	if reply.Score == nil {
		return &spec.CheckRunResults{}, nil
	}
	return reply.Score, nil
}

// buildImageFn builds the target image from the per-run repo into tag via the "cli"
// host seam (`charly -C <repo> box build <img> --tag <tag>`). The image-baked
// scoring path (below) is dead for iterate entities (they always carry a ScoringPlan
// → the live-plan path); kept faithful for the source-only rebuild case.
func buildImageFn(ctx context.Context, repoDir, image, tag, logPath string) (time.Duration, error) {
	start := time.Now()
	reply, err := bedCli(cmdExec, ctx, true, "-C", repoDir, "box", "build", image, "--tag", tag)
	if logPath != "" {
		_ = os.WriteFile(logPath, []byte(reply.Stdout), 0o644)
	}
	if err != nil {
		return time.Since(start), err
	}
	if reply.ExitCode != 0 {
		return time.Since(start), fmt.Errorf("box build %s exited %d: %s", image, reply.ExitCode, reply.Error)
	}
	return time.Since(start), nil
}

// runCharlyImageTestFn shells out to `charly check box <tag> --format yaml` via the
// "cli" host seam. The yaml scorer payload is on stdout (the header on stderr), so
// Capture=true returns exactly the bytes the scorer parses.
func runCharlyImageTestFn(ctx context.Context, tag string) ([]byte, time.Duration, error) {
	start := time.Now()
	reply, err := bedCli(cmdExec, ctx, true, "check", "box", tag, "--format", "yaml")
	if err != nil {
		return nil, time.Since(start), err
	}
	if reply.ExitCode != 0 {
		return []byte(reply.Stdout), time.Since(start), fmt.Errorf("check box %s exited %d: %s", tag, reply.ExitCode, reply.Error)
	}
	return []byte(reply.Stdout), time.Since(start), nil
}

// RunnerStreamConfig customizes runRunnerFn's stdout/stderr handling for AIs that
// emit structured output.
type RunnerStreamConfig struct {
	OutputFormat string            // "" | "stream-json"
	NdjsonPath   string            // stream-json only — raw NDJSON tee
	StderrPath   string            // stream-json only — separate stderr file
	OnEvent      func(RunnerEvent) // stream-json only — called per parsed line
}

// runRunnerFn invokes the resolved AI runner inside the active target (plugin-local
// exec of the agent CLI argv). When stream is stream-json, stdout is teed+parsed and
// stderr split into a sibling file; otherwise stdout+stderr merge into logPath.
func runRunnerFn(ctx context.Context, layout RunLayout, argv []string, env map[string]string, logPath string, stream *RunnerStreamConfig) (time.Duration, error) {
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

// RunHarness executes the iteration loop against opts and returns the final report.
// Caller is responsible for creating the per-run clone and collecting the pre-AI
// baseline (those happen in CheckRunLocalCmd.Run, inside the target).
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

		if opts.DryRun {
			report.ExitReason = "dry-run"
			break
		}

		iterState.ScoreDelta = iterState.Score - prevScore
		report.Iterations[len(report.Iterations)-1].ScoreDelta = iterState.ScoreDelta

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

		if opts.PlateauIteration > 0 && plateauCounter >= opts.PlateauIteration {
			report.ExitReason = "plateau"
			break
		}

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
func stepIDSet(steps []spec.StepScore) map[string]bool {
	out := make(map[string]bool, len(steps))
	for _, s := range steps {
		out[s.ID] = true
	}
	return out
}

// stillUnsolved returns scored steps still in play across the run so far.
func stillUnsolved(pre []spec.StepScore, iters []IterationState) []spec.StepScore {
	latest := make(map[string]Verdict)
	for _, it := range iters {
		for _, v := range it.Step {
			latest[v.ID] = v.Verdict
		}
	}
	var out []spec.StepScore
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
	unsolved []spec.StepScore,
	reportSoFar *FinalReport,
	prevScore int,
	plateauCounterEntering int,
	benchmarkStart time.Time,
) (iter IterationState, err error) {
	iter = IterationState{K: k, Phase: opts.Phase}
	iterStart := time.Now().UTC()
	iter.StartedUTC = iterStart.Format(time.RFC3339)
	defer func() {
		finished := time.Now().UTC()
		iter.FinishedUTC = finished.Format(time.RFC3339)
		iter.IterationDuration = finished.Sub(iterStart).String()
	}()
	var iterMu sync.Mutex
	iterDir := layout.IterDir(k)
	if err := os.MkdirAll(iterDir, 0o755); err != nil {
		return iter, fmt.Errorf("mkdir iter%d: %w", k, err)
	}

	// 0. Pre-iter fixture-persistence check (iter ≥ 2 only): warn if an in-scope
	// step's pod disappeared. Don't auto-redeploy — that's the AI's job.
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
	planYAML := RenderPlanYAML(opts.MergedPlan)
	checksYAML := RenderPlanYAML(unsolvedPlanSubset(opts.MergedPlan, unsolved))
	deploymentName := opts.Deploy
	scoreDelta := 0
	if k > 1 {
		scoreDelta = priorScore(reportSoFar) - prevScore
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

	if opts.DryRun {
		return iter, nil
	}

	return dispatchRunnerAndScore(ctx, opts, layout, k, iterDir, substCtx, promptText, reportSoFar, benchmarkStart, iter, &iterMu)
}

// dispatchRunnerAndScore invokes the AI runner under an optional per-iteration
// timeout + score-progress watchdog, then scores the result and classifies every
// step into the iteration record.
//
//nolint:gocyclo // cohesive sequential pipeline already split out of runOneIteration
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

	timeout, _ := ParseAgentTimeout(opts.Agent.Timeout)
	var runnerCtx context.Context
	var cancelRunner context.CancelFunc
	if timeout > 0 {
		runnerCtx, cancelRunner = context.WithTimeout(ctx, timeout)
	} else {
		runnerCtx, cancelRunner = context.WithCancel(ctx)
	}

	// Score-progress watchdog — hidden from the AI. Probes the live deployments via
	// the "score" seam every CheckInterval; terminates the runner if the score has
	// not improved in NoImprovementTimeout. Only applies when ScoringPlan is
	// non-empty (live-plan scoring).
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
			scoreName := opts.ScoreName
			phase, phaseTotal, iterK := opts.Phase, opts.PhaseTotal, k
			stderr := opts.Stderr
			wd := &ProgressWatchdog{
				CheckInterval:        checkInterval,
				NoImprovementTimeout: noImpTimeout,
				BenchmarkStart:       benchmarkStart,
				Probe: func(probeCtx context.Context) (int, int, error) {
					live, err := scoreLive(probeCtx, scoreName, scoringPlan)
					if err != nil {
						return 0, 0, err
					}
					return live.Summary.Pass, live.Summary.Total, nil
				},
				OnTick: func(elapsed time.Duration, score, total int, lastImprovedAt time.Time) {
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

	iter.RunnerCommand = append([]string(nil), runnerArgv...)

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
		iter.RunnerLogPath = runnerLog
		if data, err := os.ReadFile(runnerLog); err == nil {
			iter.RunnerOutput = string(data)
		}
	}
	if runnerErr != nil {
		fmt.Fprintf(opts.Stderr, "iter%d: runner exited with error: %v (continuing)\n", k, runnerErr)
	}

	// 4. Score against the substituted plan.
	useLivePlan := len(opts.ScoringPlan) > 0
	iterTagSuffix := fmt.Sprintf("charlycheck-%s-iter%d", layout.RunID, k)
	iterRef := fmt.Sprintf("ghcr.io/opencharly/%s:%s", opts.TargetImage, iterTagSuffix)
	var (
		testOut             []byte
		parsed              *spec.CheckRunResults
		postFingerprints    map[string]string
		postTagFingerprints map[string]string
	)

	if useLivePlan {
		testStart := time.Now()
		live, scoreErr := scoreLive(ctx, opts.ScoreName, opts.ScoringPlan)
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
		postSet := loadDescriptionsFromDir(ctx, opts.ScoreName)
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

	postByID := stepScoresByID(parsed)
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

// commitIterationBestEffort commits the iteration in the per-run clone, after
// emitting a per-iter delta summary and killing issue-52328 orphan poll-loop bashes.
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

// emitIterEndSummary prints one stderr line at the end of every iteration with a
// per-iter delta breakdown.
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

// stepShortName returns the tail segment of a step id.
func stepShortName(id string) string {
	parts := strings.Split(id, ":")
	if len(parts) >= 2 {
		return parts[len(parts)-2] + ":" + parts[len(parts)-1]
	}
	return id
}

// warnMissingInScopePods probes the running container set and warns once per missing
// fixture pod referenced by the in-scope plan steps' Op.Venue. Best-effort.
func warnMissingInScopePods(plan []spec.Step) {
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

// killOrphanLoopBashes kills issue-52328 deadlock orphans inside the target's PID
// namespace (pod targets only) via `podman exec <container> pkill`.
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
		cmd := exec.Command("podman", "exec", container, "pkill", "-c", "-f", pat)
		out, _ := cmd.Output()
		var n int
		_, _ = fmt.Sscanf(string(out), "%d", &n) // best-effort: parse failure leaves n=0
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

// unsolvedPlanSubset returns the plan steps whose id is in the unsolved set, for the
// ${CHECKS} prompt token.
func unsolvedPlanSubset(plan []spec.Step, unsolved []spec.StepScore) []spec.Step {
	want := make(map[string]bool, len(unsolved))
	for _, u := range unsolved {
		want[u.ID] = true
	}
	var out []spec.Step
	for i := range plan {
		id := kit.EffectiveStepID(&plan[i], scoredPlanOrigin, i)
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

// RenderPlanYAML returns the plan rendered as a YAML block for ${PLAN}.
func RenderPlanYAML(plan []spec.Step) string {
	if len(plan) == 0 {
		return ""
	}
	var buf strings.Builder
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(plan); err != nil {
		return fmt.Sprintf("# error rendering plan: %v", err)
	}
	_ = enc.Close()
	return buf.String()
}

// ---------------------------------------------------------------------------
// Scope rendering
// ---------------------------------------------------------------------------

// HarnessScope is the YAML-serializable form of .check/scope.yml.
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
func renderScope(opts HarnessOpts, layout RunLayout, k int, reportSoFar *FinalReport, unsolved []spec.StepScore) *HarnessScope {
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
	env["CHARLY_EVAL_PHASE"] = fmt.Sprintf("%d", substCtx.Phase)
	if opts.Iterate != nil && opts.Iterate.NotesEnabled() {
		harnessRoot := HarnessDataRoot(opts.ProjectDir, opts.ScoreName)
		env["CHARLY_EVAL_NOTES_FILE"] = NotePathForRun(harnessRoot, substCtx.RunID)
	}
	return argv, env
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
