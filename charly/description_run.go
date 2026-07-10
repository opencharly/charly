package main

import (
	"context"

	"github.com/opencharly/sdk/kit"
)

// StepResult pairs a CheckResult with the step's keyword/text + stable id.
// It is the runner's per-step output unit (one per executed plan step).
// StepResult lives in sdk/kit (checkresult.go) with the rest of the result model; this is
// the package-main binding.
type StepResult = kit.StepResult

// RunPlan executes the flat plan in a LabelDescriptionSet (already collected +
// include-expanded + overlay-merged) against the runner, returning per-step
// results for reporting + scoring.
//
// The plan walk itself — flatten by layer, mode-gated step selection, agent-step
// grading, and per-step dispatch through RunOne — lives in sdk/kit (planrun.go).
// The Runner is its host driver via the runnerPlanContext adapter; the unused
// *TagExpr keeps the package-main signature its callers pass (tag filtering is
// applied upstream at collection time).
func RunPlan(ctx context.Context, r *Runner, set *LabelDescriptionSet, _ *TagExpr, strict bool) []StepResult {
	return kit.RunPlan(ctx, runnerPlanContext{r: r}, set, strict)
}
