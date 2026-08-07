package main

// plan_unify_test.go — the §J failing-first acceptance suite for the plan-unify
// cutover: the unified flat `plan:` vocabulary (run/check/agent-*/include), the
// runner modes, the per-step scorer, the include splice, and the
// run-step → install-step lowering.
//
// §J.1/§J.2/§J.3's kit.RunPlan-semantics tests (TestPlanUnify_CheckStepRuns,
// TestPlanUnify_VerifyOnlySkipsRun, TestPlanUnify_SkipDeterministicRunSkipsInstall) relocated to
// sdk/kit's own test suite (#55 decoupling cone, Batch D — TestRunPlan_VerifyOnlySkipsMutating
// already covered the VerifyOnly case; TestRunPlan_SkipDeterministicRunSkipsInstall is new in
// sdk/kit/planrun_dispatch_test.go): each asserted kit.RunPlan's OWN keyword/mode semantics using
// "matching"/"command" verbs only as incidental payloads, with zero real charly dispatch
// coupling once driven through a stub VerbResolver instead of a directly-constructed charly
// hostVerbResolver (the construction path production no longer uses).

import (
	"testing"

	"github.com/opencharly/spec/spec"
)

// §J.3 — the no-check-step ADE rejection (validateCandyContents) moved with the validate engine to
// candy/plugin-box (task #60); it is re-expressed as an on-disk fixture through the real
// `charly box validate` gate in validate_fixture_test.go (TestValidate_RejectsNoCheckStep).

// §J.6 — the `include: <kind>:<name>` candy/box/pod/vm plan-splice arms relocated to
// candy/plugin-check (the include-splicer now reads the resolved-project envelope, not the core
// loader). Coverage lives in candy/plugin-check/checkproject_test.go.

// §J.8 — a migrated task:→run: step lowers to an InstallStep AND reverses on
// `charly fleet del` (the task:→plan: fold preserves the ledger/reversal).
func TestPlanUnify_RunStepLowersToInstallStepAndReverses(t *testing.T) {
	// The migration turns a `task: { package: redis }` op into a run: step. `package` is
	// now an extracted plugin verb (plugin: package + plugin_input), whose TypedStepProvider
	// lowers the run-act into the same SystemPackagesStep.
	layer := testCandy("x", spec.CandyModel{Plan: []spec.Step{{Run: "install redis", Op: spec.Op{Plugin: "package", PluginInput: map[string]any{"package": "redis"}}}}}, spec.CandyView{})
	steps := testCompileOpSteps(t, layer)

	var sp *spec.SystemPackagesStep
	for _, s := range steps {
		if v, ok := s.(*spec.SystemPackagesStep); ok {
			sp = v
		}
	}
	if sp == nil {
		t.Fatalf("run: package step did not lower to a SystemPackagesStep: %#v", steps)
	}
	rev := sp.Reverse()
	if len(rev) != 1 || rev[0].Kind != spec.ReverseOpPackageRemove {
		t.Fatalf("lowered install step does not reverse to package-remove (charly fleet del): %+v", rev)
	}
}
