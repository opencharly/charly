package main

import (
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// description_collect.go — collect the baked `plan:` view for the
// ai.opencharly.description OCI label.
//
// CollectDescriptions walks the base-box chain for boxName and gathers every
// kind: entity's plan: into a three-section LabelDescriptionSet. The walk
// mirrors CollectHooks and CollectShell: candy-order per level, then step into
// internal base, dedupe by candy name, stop at first external base OR on cycle.
//
// Bake rule (what goes IN the label): the VERIFICATION + runtime-provisioning
// view of plan: — every check:/agent-check: step plus any run: step whose
// context: includes runtime (plan-runtime provisioning a checker needs).
// Pure build/deploy-context run: steps (the install timeline) are NOT baked —
// they are consumed by the InstallPlan→Containerfile/DeployExecutor and are
// already materialized in the image. agent-run:/include: steps are not baked
// (agent-run appears only in deploy-level iterate: plans; include is expanded
// into iterate plans, never a candy bake).
//
// MergeDeployDescriptions (the per-host deploy-plan overlay onto a baked
// LabelDescriptionSet) moved to sdk/kit (description_merge.go) — P12a follow-up:
// pure over LabelDescriptionSet/spec.Step, zero core state. Its callers
// (check_cmd.go's checkLivePod, the "live" gather engine) stay core (they need
// LoadUnified/ExtractMetadata) and call kit.MergeDeployDescriptions.

// bakeableSteps returns the subset of a plan that belongs in the runtime
// descriptor label per the bake rule above.
func bakeableSteps(plan []spec.Step) []spec.Step {
	var out []spec.Step
	for _, s := range plan {
		bake := false
		switch {
		case s.Check != "" || s.AgentCheck != "":
			bake = true
		case s.Run != "" && opInContext(&s.Op, spec.CtxRuntime):
			bake = true
		}
		if !bake {
			continue
		}
		// DELIBERATE collect-time stamp: write the keyword-derived do-mode onto the baked COPY so
		// the ai.opencharly.description label carries intent_do. This was formerly a side effect of
		// the in-core validate mutating the shared structs the bake serialized; when the validate
		// ENGINE moved to candy/plugin-box (K3-D+) it began stamping only its envelope copy, so the
		// bake must now stamp its own output (verb-less agent-check steps keep an empty IntentDo).
		stampStepIntentDo(&s)
		out = append(out, s)
	}
	return out
}

// CollectDescriptions returns nil if every section is empty.
func CollectDescriptions(cfg *Config, layers map[string]spec.CandyReader, boxName string) *spec.LabelDescriptionSet {
	set := &spec.LabelDescriptionSet{}

	allCandyNames, _ := deploykit.BoxCandyChain(cfg, layers, boxName)
	for _, candyName := range allCandyNames {
		layer, ok := layers[candyName]
		if !ok {
			continue
		}
		baked := bakeableSteps(layer.PlanSteps())
		if layer.GetDescription() == "" && len(baked) == 0 {
			continue
		}
		set.Candy = append(set.Candy, spec.LabeledDescription{
			Origin:      "candy:" + candyName,
			Description: layer.GetDescription(),
			Plan:        baked,
		})
	}

	// Box-level description + plan.
	if img, ok := cfg.BoxConfig(boxName); ok {
		baked := bakeableSteps(img.Plan)
		if img.Description != "" || len(baked) > 0 {
			set.Box = append(set.Box, spec.LabeledDescription{
				Origin:      "box:" + boxName,
				Description: img.Description,
				Plan:        baked,
			})
		}
	}

	if set.IsEmpty() {
		return nil
	}
	return set
}
