package main

// plan_unify_test.go — the §J failing-first acceptance suite for the plan-unify
// cutover: the unified flat `plan:` vocabulary (run/check/agent-*/include), the
// runner modes, the per-step scorer, the include splice, and the
// run-step → install-step lowering.

import (
	"context"
	"strings"
	"testing"

	"github.com/opencharly/sdk/kit"
)

// §J.1 — a check: step runs and reports its verdict through RunPlan.
func TestPlanUnify_CheckStepRuns(t *testing.T) {
	set := &LabelDescriptionSet{Candy: []LabeledDescription{{
		Origin: "candy:x",
		Plan: []Step{{Check: "the marker resolves", Op: Op{
			Plugin:      "matching",
			PluginInput: map[string]any{"matching": "charly-marker", "contains": map[string]any{"contains": "charly-marker"}},
		}}},
	}}}
	r := newCheckRunner(kit.RunnerConfig{Mode: RunModeLive})
	res := kit.RunPlan(context.Background(), r, set, false)
	if len(res) != 1 {
		t.Fatalf("want 1 step result, got %d", len(res))
	}
	if res[0].Keyword != string(kit.KwCheck) {
		t.Errorf("keyword = %q, want check", res[0].Keyword)
	}
	if res[0].Result.Status != TestPass {
		t.Fatalf("check step should pass, got %v (%s)", res[0].Result.Status, res[0].Result.Message)
	}
}

// §J.2 — VerifyOnly mode skips run: (mutating) steps and runs check: steps.
func TestPlanUnify_VerifyOnlySkipsRun(t *testing.T) {
	set := &LabelDescriptionSet{Candy: []LabeledDescription{{
		Origin: "candy:x",
		Plan: []Step{
			{Run: "mutate the world", Op: cmdOp("echo should-not-run")},
			{Check: "the marker resolves", Op: Op{
				Plugin:      "matching",
				PluginInput: map[string]any{"matching": "m", "contains": map[string]any{"contains": "m"}},
			}},
		},
	}}}
	r := newCheckRunner(kit.RunnerConfig{Mode: RunModeLive, VerifyOnly: true})
	res := kit.RunPlan(context.Background(), r, set, false)
	if len(res) != 2 {
		t.Fatalf("want 2 step results, got %d", len(res))
	}
	// The run: step is skipped (not executed) under verify-only.
	if res[0].Keyword != string(kit.KwRun) || res[0].Result.Status != TestSkip {
		t.Errorf("run: step should be skipped under VerifyOnly, got keyword=%q status=%v", res[0].Keyword, res[0].Result.Status)
	}
	if !strings.Contains(res[0].Result.Message, "verify-only") {
		t.Errorf("skip reason should name verify-only, got %q", res[0].Result.Message)
	}
	// The check: step still runs and passes.
	if res[1].Keyword != string(kit.KwCheck) || res[1].Result.Status != TestPass {
		t.Errorf("check: step should run under VerifyOnly, got keyword=%q status=%v", res[1].Keyword, res[1].Result.Status)
	}
}

// §J.3 — feature-run's SkipDeterministicRun skips DETERMINISTIC run: install
// steps (so build-context installs like `pip install /ctx/...` are not
// re-executed at acceptance against a built/deployed target), while still
// running check: and routing agent-run: to the grader path (NOT swept up as an
// install step). Regression for #16: feature-run re-ran run: steps, so a
// jupyter-mcp `pip install --no-deps /ctx/jupyter_mcp` step failed against the
// live pod (/ctx exists only during image-build).
func TestPlanUnify_SkipDeterministicRunSkipsInstall(t *testing.T) {
	set := &LabelDescriptionSet{Candy: []LabeledDescription{{
		Origin: "candy:x",
		Plan: []Step{
			{Run: "pip install /ctx/pkg", Op: cmdOp("false")}, // would FAIL if executed
			{Check: "the marker resolves", Op: Op{
				Plugin:      "matching",
				PluginInput: map[string]any{"matching": "m", "contains": map[string]any{"contains": "m"}},
			}},
			{AgentRun: "an agent drives the UI", Op: Op{}}, // agent step, NOT a deterministic install
		},
	}}}
	r := newCheckRunner(kit.RunnerConfig{Mode: RunModeLive, SkipDeterministicRun: true}) // the `charly check feature run` (ADE acceptance) mode
	res := kit.RunPlan(context.Background(), r, set, false)
	if len(res) != 3 {
		t.Fatalf("want 3 step results, got %d", len(res))
	}
	// The deterministic run: install step is skipped (would FAIL with `false` if executed).
	if res[0].Keyword != string(kit.KwRun) || res[0].Result.Status != TestSkip {
		t.Errorf("run: install step should be skipped under SkipDeterministicRun, got keyword=%q status=%v", res[0].Keyword, res[0].Result.Status)
	}
	if !strings.Contains(res[0].Result.Message, "install-timeline") {
		t.Errorf("skip reason should name the install-timeline, got %q", res[0].Result.Message)
	}
	// The check: step still runs and passes.
	if res[1].Keyword != string(kit.KwCheck) || res[1].Result.Status != TestPass {
		t.Errorf("check: step should run, got keyword=%q status=%v", res[1].Keyword, res[1].Result.Status)
	}
	// agent-run: is NOT skipped as a deterministic install step — it reaches the
	// agent path (no grader bound here → advisory skip with the agent reason, not
	// the install-timeline reason).
	if strings.Contains(res[2].Result.Message, "install-timeline") {
		t.Errorf("agent-run: must NOT be skipped as a deterministic install step, got %q", res[2].Result.Message)
	}
}

// §J.3 — the no-check-step ADE rejection (validateCandyContents) moved with the validate engine to
// candy/plugin-box (task #60); it is re-expressed as an on-disk fixture through the real
// `charly box validate` gate in validate_fixture_test.go (TestValidate_RejectsNoCheckStep).

// §J.6 — the `include: <kind>:<name>` candy/box/pod/vm plan-splice arms relocated to
// candy/plugin-check (the include-splicer now reads the resolved-project envelope, not the core
// loader). Coverage lives in candy/plugin-check/checkproject_test.go.

// §J.8 — a migrated task:→run: step lowers to an InstallStep AND reverses on
// `charly bundle del` (the task:→plan: fold preserves the ledger/reversal).
func TestPlanUnify_RunStepLowersToInstallStepAndReverses(t *testing.T) {
	// The migration turns a `task: { package: redis }` op into a run: step. `package` is
	// now an extracted plugin verb (plugin: package + plugin_input), whose TypedStepProvider
	// lowers the run-act into the same SystemPackagesStep.
	layer := &Candy{Name: "x", plan: []Step{{Run: "install redis", Op: Op{Plugin: "package", PluginInput: map[string]any{"package": "redis"}}}}}
	steps := compileOpSteps(layer, testResolvedBox())

	var sp *SystemPackagesStep
	for _, s := range steps {
		if v, ok := s.(*SystemPackagesStep); ok {
			sp = v
		}
	}
	if sp == nil {
		t.Fatalf("run: package step did not lower to a SystemPackagesStep: %#v", steps)
	}
	rev := sp.Reverse()
	if len(rev) != 1 || rev[0].Kind != ReverseOpPackageRemove {
		t.Fatalf("lowered install step does not reverse to package-remove (charly bundle del): %+v", rev)
	}
}
