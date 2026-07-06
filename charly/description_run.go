package main

import (
	"context"
	"os"
	"strings"
	"syscall"
)

// StepResult pairs a CheckResult with the step's keyword/text + stable id.
// It is the runner's per-step output unit (one per executed plan step).
type StepResult struct {
	Keyword string      `json:"keyword"`
	Text    string      `json:"text"`
	Origin  string      `json:"origin,omitempty"`
	StepID  string      `json:"step_id"`
	Result  CheckResult `json:"result"`
}

// flatStep carries a plan step with its collection-time origin + the owning
// entity's description (for the agent grader).
type flatStep struct {
	origin string
	desc   string
	idx    int
	step   Step
}

// RunPlan executes the flat plan in a LabelDescriptionSet (already collected +
// include-expanded + overlay-merged) against the runner, returning per-step
// results for reporting + scoring.
//
// The Runner mode selects which steps execute:
//   - VerifyOnly (charly check live / box): check:/agent-check: only — Mutates()
//     steps (run:/agent-run:) are skipped.
//   - provision-and-verify (default): every step in declaration order.
//
// include: steps never reach here (expanded at collect time); a residual one
// is a no-op skip. Agent steps route to the grader; run/check stamp the
// keyword-derived intentDo and dispatch through runOne.
func RunPlan(ctx context.Context, r *Runner, set *LabelDescriptionSet, _ *TagExpr, strict bool) []StepResult {
	if set == nil {
		return nil
	}
	var flat []flatStep
	for _, sec := range [][]LabeledDescription{set.Candy, set.Box, set.Deploy} {
		for _, ld := range sec {
			for i, s := range ld.Plan {
				flat = append(flat, flatStep{origin: ld.Origin, desc: ld.Description, idx: i, step: s})
			}
		}
	}

	planCtx := NewScenarioContext()
	orig := r.Scenario
	r.Scenario = planCtx
	defer func() { r.Scenario = orig }()

	var out []StepResult
	for _, fs := range flat {
		stepID := EffectiveStepID(&fs.step, fs.origin, fs.idx)
		out = append(out, runUnit(ctx, r, fs, planCtx, stepID, strict))
	}

	// Reap host-side background processes spawned by command: steps.
	for _, pid := range planCtx.SnapshotBackgrounds() {
		_ = sendSIGTERM(pid)
	}
	return out
}

// runUnit executes one plan step and returns its result.
func runUnit(ctx context.Context, r *Runner, fs flatStep, stepCtx *ScenarioContext, stepID string, strict bool) StepResult {
	step := fs.step
	sr := StepResult{
		Keyword: string(keywordOf(&step)),
		Text:    step.KeywordText(),
		Origin:  fs.origin,
		StepID:  stepID,
	}

	// include: steps were spliced at collect time — a residual one is a no-op.
	if step.IsInclude() {
		sr.Result = CheckResult{Status: TestSkip, Message: "include expanded at collect time"}
		return sr
	}

	// VerifyOnly: skip mutating steps (run:/agent-run:).
	if r.VerifyOnly && step.Mutates() {
		sr.Result = CheckResult{Status: TestSkip, Message: "skipped — verify-only mode (mutating step)"}
		return sr
	}

	// feature-run (ADE acceptance "Run"): skip the DETERMINISTIC run:
	// install-timeline steps (Mutates but not an agent step). The install ran
	// at image-build; re-executing it against a built/deployed target is
	// redundant and fails for build-context steps (e.g. `pip install /ctx/...`,
	// where /ctx exists only during the Containerfile build). feature-run
	// verifies via check:/agent-check: and still grades agent-run: (IsAgent,
	// not skipped here). See /charly-check:check ADE + checkrun.go
	// SkipDeterministicRun.
	if r.SkipDeterministicRun && step.Mutates() && !step.IsAgent() {
		sr.Result = CheckResult{Status: TestSkip, Message: "skipped — run: install-timeline step (feature-run verifies, does not re-install)"}
		return sr
	}

	// Agent steps route to the grader (read-only for agent-check).
	if step.IsAgent() {
		if r.Grader != nil {
			sr.Result = r.Grader.Grade(ctx, GraderRequest{
				Description: fs.desc,
				Keyword:     string(keywordOf(&step)),
				Text:        step.KeywordText(),
				ReadOnly:    !step.Mutates(),
			})
			return sr
		}
		status := TestSkip
		msg := "agent step (no grader bound)"
		if strict {
			status = TestFail
			msg = "agent step (no grader bound) — strict mode"
		}
		sr.Result = CheckResult{Status: status, Message: msg, Verb: "agent"}
		return sr
	}

	// run:/check: — stamp the keyword-derived do-mode + the owning entity's
	// origin (the candy/box/deploy key this step came from), then dispatch to
	// runOne. The per-step Op.Origin is NOT baked into the OCI label (the origin
	// lives once on the LabeledDescription group), so it MUST be re-stamped here
	// from the flattened group origin — runOne consumers rely on it (e.g.
	// resolveCheckApk anchors a candy's committed APK against CandyDirs[origin]).
	op := step.Op
	op.Origin = fs.origin
	op.IntentDo = string(stepDoMode(&step))
	stepCtx.CurrentStepID = stepID
	checkRes := r.runOne(ctx, &op)
	sr.Result = checkRes
	return sr
}

// keywordOf returns the populated step keyword, or "" when none is set.
func keywordOf(s *Step) StepKeyword {
	if k, err := s.StepKind(); err == nil {
		return k
	}
	return ""
}

// sendSIGTERM sends SIGTERM to a host-side PID. Best-effort.
func sendSIGTERM(pid int) error {
	if pid <= 0 {
		return nil
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return nil
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		if strings.Contains(err.Error(), "process already finished") ||
			strings.Contains(err.Error(), "no such process") {
			return nil
		}
		return err
	}
	return nil
}
