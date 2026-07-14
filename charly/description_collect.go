package main

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

// bakeableSteps returns the subset of a plan that belongs in the runtime
// descriptor label per the bake rule above.
func bakeableSteps(plan []Step) []Step {
	var out []Step
	for _, s := range plan {
		bake := false
		switch {
		case s.Check != "" || s.AgentCheck != "":
			bake = true
		case s.Run != "" && opInContext(&s.Op, CtxRuntime):
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
func CollectDescriptions(cfg *Config, layers map[string]*Candy, boxName string) *LabelDescriptionSet {
	set := &LabelDescriptionSet{}

	allCandyNames, _ := cfg.boxCandyChain(layers, boxName)
	for _, candyName := range allCandyNames {
		layer, ok := layers[candyName]
		if !ok {
			continue
		}
		baked := bakeableSteps(layer.plan)
		if layer.Description == "" && len(baked) == 0 {
			continue
		}
		set.Candy = append(set.Candy, LabeledDescription{
			Origin:      "candy:" + candyName,
			Description: layer.Description,
			Plan:        baked,
		})
	}

	// Box-level description + plan.
	if img, ok := cfg.BoxConfig(boxName); ok {
		baked := bakeableSteps(img.Plan)
		if img.Description != "" || len(baked) > 0 {
			set.Box = append(set.Box, LabeledDescription{
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

// MergeDeployDescriptions overlays a deployment node's local `plan:` steps onto
// a label-baked LabelDescriptionSet's Deploy section. A baked deploy step with
// the same step id is replaced by the local one; otherwise the local step is
// appended. This is the per-host override surface for acceptance steps
// (charly.yml deploy entries). If localPlan is empty, returns baked unchanged.
func MergeDeployDescriptions(baked *LabelDescriptionSet, localPlan []Step, originName string) *LabelDescriptionSet {
	if len(localPlan) == 0 {
		return baked
	}
	if baked == nil {
		baked = &LabelDescriptionSet{}
	}
	// Index baked deploy steps by author id for replace-by-id (only steps
	// carrying an explicit Op.ID participate; derived ids are positional and
	// not stable across an overlay).
	type loc struct{ ld, st int }
	locByID := map[string]loc{}
	for li := range baked.Deploy {
		for si := range baked.Deploy[li].Plan {
			if id := baked.Deploy[li].Plan[si].ID; id != "" {
				locByID[id] = loc{li, si}
			}
		}
	}
	var fresh []Step
	for _, st := range localPlan {
		if id := st.ID; id != "" {
			if l, ok := locByID[id]; ok {
				baked.Deploy[l.ld].Plan[l.st] = st // replace by id
				continue
			}
		}
		fresh = append(fresh, st)
	}
	if len(fresh) > 0 {
		baked.Deploy = append(baked.Deploy, LabeledDescription{
			Origin: "deploy-local:" + originName,
			Plan:   fresh,
		})
	}
	return baked
}
