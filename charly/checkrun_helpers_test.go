package main

import (
	"context"
	"testing"

	specexec "github.com/opencharly/spec/exec"
	"github.com/opencharly/spec/spec"
)

// checkrun_helpers_test.go — the check-engine unit-test DISPATCH helper.
//
// Production charly core no longer constructs an in-proc plan-walk engine at all: the deploy-scope
// check DRIVE (variable expansion, `eventually:` retry, per-probe never-hang, exclude_distros/context
// gating, and — for a builtin verb — the actual RunVerb dispatch) moved PLUGIN-SIDE in the #55
// CHECK-ENGINE cone (Unit 2): command:check's OpVerifyChecks handler (check_cmd.go's
// dispatchVerifyChecks) threads a live venue executor to the compiled-in candy/plugin-check, which
// builds its OWN engine and reaches BACK into charly core's hostVerbResolver/providerRegistry over the
// InvokeProvider reverse channel for every builtin verb.
//
// This helper drives the handful of check-engine tests that genuinely assert CHARLY's OWN dispatch
// (e.g. the context-vs-mode skip gate, hostPlanGrammar/opInContext) through that SAME production seam
// — dispatchVerifyChecks against a REAL local shell venue (specexec.ShellExecutor{}), never a
// directly-constructed kit.Runner (which would test a construction path production no longer uses).
// Tests asserting kit.RunOne/kit.RunPlan's OWN wrapper semantics, or a specific verb's OWN RunVerb
// logic, moved to sdk/kit's own test suite / each verb's own candy module respectively (R3 — see the
// #55 decoupling cone Batch D report for the full per-file breakdown).

// dispatchCheckOpsMode drives ops (in the given mode: "live"/"box") through command:check's
// OpVerifyChecks deploy-scope check-verify drive (dispatchVerifyChecks, check_cmd.go) against a
// real local shell venue, returning the per-op spec.CheckResult in order.
func dispatchCheckOpsMode(t *testing.T, mode string, ops []spec.Op) []spec.CheckResult {
	t.Helper()
	results, err := dispatchVerifyChecks(context.Background(), specexec.ShellExecutor{}, spec.VerifyChecksRequest{
		Ops: ops, Mode: mode,
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
