package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
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
	i := 0
	for i < len(flat) {
		// Parallel group: consecutive steps sharing a non-empty Op.Parallel id.
		groupID := flat[i].step.Parallel
		end := i + 1
		if groupID != "" {
			for end < len(flat) && flat[end].step.Parallel == groupID && flat[end].origin == flat[i].origin {
				end++
			}
		}
		isParallel := groupID != "" && end-i > 1
		out = append(out, runFlatGroup(ctx, r, flat[i:end], planCtx, strict, isParallel)...)
		i = end
	}

	// Reap host-side background processes spawned by command: steps.
	for _, pid := range planCtx.SnapshotBackgrounds() {
		_ = sendSIGTERM(pid)
	}
	return out
}

// runFlatGroup runs one or more sibling flat steps, optionally in parallel,
// honoring Op.Count expansion.
func runFlatGroup(ctx context.Context, r *Runner, group []flatStep, stepCtx *ScenarioContext, strict, parallel bool) []StepResult {
	type unit struct {
		fs     flatStep
		subIdx int // -1 if no count expansion
		stepID string
	}
	var units []unit
	for _, fs := range group {
		count := fs.step.Count
		baseID := EffectiveStepID(&fs.step, fs.origin, fs.idx)
		if count <= 0 {
			units = append(units, unit{fs: fs, subIdx: -1, stepID: baseID})
			continue
		}
		for j := range count {
			cp := fs
			cp.step.ID = appendIndex(cp.step.ID, j)
			cp.step.Capture = appendIndex(cp.step.Capture, j)
			units = append(units, unit{fs: cp, subIdx: j, stepID: fmt.Sprintf("%s-%d", baseID, j)})
		}
	}

	results := make([]StepResult, len(units))
	run := func(u unit, slot int) {
		results[slot] = runUnit(ctx, r, u.fs, stepCtx, u.stepID, u.subIdx, strict)
	}

	if parallel && len(units) > 1 {
		var wg sync.WaitGroup
		for i, u := range units {
			wg.Add(1)
			i, u := i, u
			go func() {
				defer wg.Done()
				run(u, i)
			}()
		}
		wg.Wait()
	} else {
		for i, u := range units {
			run(u, i)
		}
	}
	return results
}

// runUnit executes one plan step and returns its result.
func runUnit(ctx context.Context, r *Runner, fs flatStep, stepCtx *ScenarioContext, stepID string, subIdx int, strict bool) StepResult {
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
	if subIdx >= 0 {
		indexVar := op.IndexVar
		if indexVar == "" {
			indexVar = "INDEX"
		}
		op = *substituteIndex(&op, indexVar, subIdx)
	}
	stepCtx.CurrentStepID = stepID
	checkRes := r.runOne(ctx, &op)
	stepCtx.RecordResult(stepID, checkRes)
	sr.Result = checkRes
	return sr
}

// appendIndex appends "-<idx>" to the input if non-empty.
func appendIndex(s string, idx int) string {
	if s == "" {
		return ""
	}
	return fmt.Sprintf("%s-%d", s, idx)
}

// substituteIndex returns a copy of op with ${<indexVar>} replaced by idx in
// every string field the runner subsequently expands.
func substituteIndex(c *Op, indexVar string, idx int) *Op {
	out := *c
	idxStr := fmt.Sprintf("%d", idx)
	tok := "${" + indexVar + "}"
	replace := func(s string) string {
		if !strings.Contains(s, tok) {
			return s
		}
		return strings.ReplaceAll(s, tok, idxStr)
	}
	out.ID = replace(out.ID)
	out.Capture = replace(out.Capture)
	out.Command = replace(out.Command)
	out.Tab = replace(out.Tab)
	out.Selector = replace(out.Selector)
	out.Text = replace(out.Text)
	out.URL = replace(out.URL)
	out.Expression = replace(out.Expression)
	out.Input = replace(out.Input)
	// http/addr/interface left #Op; their discriminator + modifiers ride plugin_input
	// (a map, not a string field). Index-var (${<indexVar>}) replication of plugin_input
	// is not performed — matching the already-extracted process/port/dns verbs, whose
	// params are likewise not index-replicated. The common runtime ${HOST_PORT:N} /
	// ${VAR} substitution in plugin_input IS handled, generically, by opExpandVars.
	return &out
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

// sendSIGKILL is the SIGKILL sibling of sendSIGTERM. Used by the kill: verb.
func sendSIGKILL(pid int) error {
	if pid <= 0 {
		return nil
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return nil
	}
	if err := proc.Signal(syscall.SIGKILL); err != nil {
		if strings.Contains(err.Error(), "process already finished") ||
			strings.Contains(err.Error(), "no such process") {
			return nil
		}
		return err
	}
	return nil
}
