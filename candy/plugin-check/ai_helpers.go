package check

// ai_helpers.go — the AI-facing iteration-helper leaves (P12: relocated from
// charly/check_runner_cmd.go). These run INSIDE an iteration (env-driven:
// CHARLY_EVAL_SCORE / RUN_ID / ITERATION): scope prints the active iteration's
// scope.yml, last-tag prints the prior iteration's image tag, self-evaluate re-runs
// the in-scope plan live via the "score" check-run mode.

import (
	"context"
	"fmt"
	"os"
)

// CheckScopeCmd reads the active iteration's scope.yml.
type CheckScopeCmd struct{}

func (c *CheckScopeCmd) Run() error {
	score := os.Getenv("CHARLY_EVAL_SCORE")
	runID := os.Getenv("CHARLY_EVAL_RUN_ID")
	iter := os.Getenv("CHARLY_EVAL_ITERATION")
	if score == "" || runID == "" || iter == "" {
		return fmt.Errorf("charly check scope: must run inside an iteration (CHARLY_EVAL_SCORE/RUN_ID/ITERATION env required)")
	}
	cwd, _ := os.Getwd()
	path := fmt.Sprintf("%s/.check/%s/runs/%s/iter%s/scope.yml", cwd, score, runID, iter)
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	_, err = os.Stdout.Write(data)
	return err
}

// CheckLastTagCmd prints the prior iteration's image tag.
type CheckLastTagCmd struct{}

func (c *CheckLastTagCmd) Run() error {
	runID := os.Getenv("CHARLY_EVAL_RUN_ID")
	iter := os.Getenv("CHARLY_EVAL_ITERATION")
	if runID == "" || iter == "" {
		return fmt.Errorf("charly check last-test-tag: must run inside an iteration")
	}
	var k int
	_, _ = fmt.Sscanf(iter, "%d", &k) // best-effort: parse failure leaves k=0, caught by the k<=1 guard
	if k <= 1 {
		return fmt.Errorf("charly check last-test-tag: no prior iteration (k=%d)", k)
	}
	fmt.Printf("charlycheck-%s-iter%d\n", runID, k-1)
	return nil
}

// CheckSelfCheckCmd implements `charly check self-evaluate` — the AI's canonical
// self-verification path during a harness iteration. It runs the SAME live scoring
// the end-of-iter harness scorer calls (the "score" check-run mode) against the
// SAME in-scope plan (derived from the PROJECT tree off the resolved-project envelope, NOT
// the per-iter repo clone — the anti-deception property: the host resolves the project
// from cwd, so AI edits to the clone don't change what self-evaluate sees).
type CheckSelfCheckCmd struct{}

func (c *CheckSelfCheckCmd) Run() error {
	score := os.Getenv("CHARLY_EVAL_SCORE")
	runID := os.Getenv("CHARLY_EVAL_RUN_ID")
	iter := os.Getenv("CHARLY_EVAL_ITERATION")
	phaseStr := os.Getenv("CHARLY_EVAL_PHASE")
	if score == "" || runID == "" || iter == "" {
		return fmt.Errorf("charly check self-evaluate: must run inside an iteration (CHARLY_EVAL_SCORE/RUN_ID/ITERATION env required)")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	reply, err := resolveCheckProjection(cmdExec, cmdCtx, score, cwd)
	if err != nil {
		return fmt.Errorf("charly check self-evaluate: load charly.yml: %w", err)
	}
	if !reply.HasNode {
		return fmt.Errorf("charly check self-evaluate: no charly.yml at project root %s — self-evaluate must run from a directory with a project tree (typically /workspace inside the harness sandbox)", cwd)
	}
	if !reply.HasIterate {
		return fmt.Errorf("charly check self-evaluate: entity %q has no iterate: block", score)
	}
	var phase int
	if phaseStr != "" {
		_, _ = fmt.Sscanf(phaseStr, "%d", &phase) // best-effort: parse failure leaves phase=0 (default phase)
	}

	plan := reply.Plan // the include-expanded scored plan, host-resolved
	if len(plan) == 0 {
		fmt.Fprintln(os.Stdout, "charly check self-evaluate: empty plan")
		return nil
	}

	ctx := context.Background()
	live, err := scoreLive(ctx, score, plan)
	if err != nil {
		return fmt.Errorf("charly check self-evaluate: live scoring: %w", err)
	}

	fmt.Fprintf(os.Stdout, "self-evaluate: score=%s phase=%d iter=%s run=%s\n", score, phase, iter, runID)
	fmt.Fprintf(os.Stdout, "%-50s  %-7s  %s\n", "STEP", "STATUS", "DETAIL")
	failed := 0
	for _, st := range live.Step {
		detail := ""
		if st.SkippedReason != "" {
			detail = st.SkippedReason
		}
		fmt.Fprintf(os.Stdout, "%-50s  %-7s  %s\n", st.Text, st.Status, detail)
		if st.Status != "pass" && st.Status != "skip" {
			failed++
		}
	}
	fmt.Fprintf(os.Stdout, "summary: %d/%d pass, %d fail, %d skip (total %d)\n",
		live.Summary.Pass, live.Summary.Total, live.Summary.Fail, live.Summary.Skip, live.Summary.Total)
	if failed > 0 {
		return fmt.Errorf("self-evaluate: %d step(s) failed", failed)
	}
	return nil
}
