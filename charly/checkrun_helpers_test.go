package main

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	specexec "github.com/opencharly/spec/exec"
	"github.com/opencharly/spec/ops"
	"github.com/opencharly/spec/spec"
)

// checkrun_helpers_test.go — the check-engine unit-test DISPATCH helper.
//
// Production charly core no longer constructs an in-proc plan-walk engine at all: the deploy-scope
// check DRIVE (variable expansion, `eventually:` retry, per-probe never-hang, exclude_distros/context
// gating, and — for a builtin verb — the actual RunVerb dispatch) moved PLUGIN-SIDE in the #55
// CHECK-ENGINE cone (Unit 2): command:check's OpVerifyChecks handler threads a live venue executor
// to the compiled-in candy/plugin-check, which builds its OWN engine and reaches BACK into charly
// core's hostVerbResolver/providerRegistry over the InvokeProvider reverse channel for every builtin
// verb.
//
// This helper drives the handful of check-engine tests that genuinely assert CHARLY's OWN dispatch
// (e.g. the context-vs-mode skip gate, opInContext) through that SAME production seam — a REAL
// local shell venue (specexec.ShellExecutor{}), never a directly-constructed kit.Runner (which would
// test a construction path production no longer uses). Tests asserting kit.RunOne/kit.RunPlan's OWN
// wrapper semantics, or a specific verb's OWN RunVerb logic, moved to sdk/kit's own test suite /
// each verb's own candy module respectively (R3 — see the #55 decoupling cone Batch D report for the
// full per-file breakdown).
//
// dispatchVerifyChecks below is the RELOCATED body of the former check_cmd.go production function
// of the same name (#55 W3 B3 remainder): its production callers (runUnifiedTargetChecks/Test) had
// zero real callers anywhere in the tree, so it moved here — its only remaining role. The former
// "ops" drive shape it used to feed died alongside Test(); this helper now wraps each Op as a
// single-step Plan instead. RunOne (sdk/kit/planrun.go) is the SAME per-step primitive both the
// deleted kit.Runner.Run(ops) path and the surviving kit.RunPlan(plan) path dispatched through, so
// the context-vs-mode gate (opInContext) this helper's tests assert is identically exercised either
// way — no coverage was lost switching shapes.

// dispatchVerifyChecks drives a deploy-scope check pass PLUGIN-SIDE via command:check's
// OpVerifyChecks, against a live venue executor threaded over the in-proc reverse channel (the
// deploy_target_dispatch.go / check_venue_resolve.go idiom), so the plugin's own verb-dispatch
// (InvokeProvider) legs reach back into this same process. The reply is the sanctioned
// []spec.StepResult wire.
func dispatchVerifyChecks(ctx context.Context, exec spec.DeployExecutor, req spec.VerifyChecksRequest) ([]spec.StepResult, error) {
	prov, ok := providerRegistry.resolve(ClassCommand, "check")
	if !ok {
		return nil, fmt.Errorf("verify-checks: command:check provider not loaded (candy/plugin-check must be compiled in via compiled_plugins:)")
	}
	req.Venue = specexec.DescriptorFromExecutor(exec)
	reqJSON, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("verify-checks: marshal request: %w", err)
	}
	invokeCtx := specexec.ContextWithExecutor(ctx,
		specexec.NewInProcExecutor(&inprocExecutorClient{srv: &executorReverseServer{exec: exec}}))
	res, err := prov.Invoke(invokeCtx, &Operation{Reserved: "check", Op: ops.OpVerifyChecks, Params: reqJSON})
	if err != nil {
		return nil, fmt.Errorf("verify-checks: command:check plugin: %w", err)
	}
	var out []spec.StepResult
	if res != nil && len(res.JSON) > 0 {
		if uerr := json.Unmarshal(res.JSON, &out); uerr != nil {
			return nil, fmt.Errorf("verify-checks: decode reply: %w", uerr)
		}
	}
	return out, nil
}

// dispatchCheckOpsMode drives checkOps (in the given mode: "live"/"box") through command:check's
// OpVerifyChecks deploy-scope check-verify drive against a real local shell venue, wrapping each Op
// as a single-step Plan (the surviving drive shape — see this file's header), and returns the
// per-op spec.CheckResult in order.
func dispatchCheckOpsMode(t *testing.T, mode string, checkOps []spec.Op) []spec.CheckResult {
	t.Helper()
	plan := make([]spec.Step, len(checkOps))
	for i, op := range checkOps {
		desc := op.ID
		if desc == "" {
			desc = fmt.Sprintf("check %s", op.Plugin)
		}
		plan[i] = spec.Step{Check: desc, Op: op}
	}
	results, err := dispatchVerifyChecks(context.Background(), specexec.ShellExecutor{}, spec.VerifyChecksRequest{
		Plan: plan, Mode: mode,
	})
	if err != nil {
		t.Fatalf("dispatchVerifyChecks: %v", err)
	}
	out := make([]spec.CheckResult, len(results))
	for i, r := range results {
		out[i] = r.Result
	}
	return out
}
