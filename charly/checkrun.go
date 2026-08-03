package main

import (
	"fmt"

	"github.com/opencharly/spec/spec"
)

// checkrun.go — the package-main RunMode binding + the check-verdict result helpers.
//
// The check-engine driver itself is kit.Runner (sdk/kit/runner.go). The host-coupled surfaces —
// the verb dispatch (hostVerbResolver), the do-mode/context grammar (hostPlanGrammar), and the
// live-verb CheckContext carrier (hostCheckCarrier) — stay in charly core (planrun_adapter.go),
// PRODUCED for the check reverse channel by plugin_dispatch_reverse.go from the wire CheckEnv
// snapshot. The IN-PROC plan-drive construction (the former newCheckRunner + carrierFromRunner +
// resolverEnv) is GONE from production: the deploy-scope check DRIVE moved PLUGIN-SIDE
// (command:check OpVerifyChecks, #55 CHECK-ENGINE cone Unit 2 — candy/plugin-check's
// newPluginCheckRunner), so charly core no longer builds a kit.Runner itself and this file no
// longer imports sdk/kit. The three former constructors are now fully DELETED (#55 decoupling
// cone, Batch D): charly-side tests drive the live dispatch surface through the PRODUCTION
// OpVerifyChecks seam instead (dispatchCheckOpsMode, checkrun_helpers_test.go); the engine's own
// semantics (variable expansion, RunPlan keyword/mode handling, ProbeNeverHang) are covered in
// sdk/kit's own test suite, and each verb's own RunVerb logic in its owning candy/plugin-<verb>
// module's plugin_test.go.
//
// RunMode selects routing rules for a check pass. It is a package-main binding onto the CUE-sourced
// spec.CheckRunMode; RunModeLive/RunModeBox map to spec.CheckModeLive/spec.CheckModeBox.
//
//   - RunModeLive: charly check live — against a running container. In-container
//     probes via Exec; host-side verbs (http/dns/addr) from the charly process.
//   - RunModeBox: charly check box — against a disposable container
//     (podman run --rm). All probes via Exec; host-side reachability is
//     not meaningful and those checks are skipped.
type RunMode = spec.CheckRunMode

const (
	RunModeLive = spec.CheckModeLive
	RunModeBox  = spec.CheckModeBox
)

// ---------------------------------------------------------------------------
// Result helpers
// ---------------------------------------------------------------------------

func passf(c *spec.Op, msg string) spec.CheckResult {
	return spec.CheckResult{Op: c, Status: spec.StatusPass, Message: msg}
}

func failf(c *spec.Op, format string, args ...any) spec.CheckResult {
	return spec.CheckResult{Op: c, Status: spec.StatusFail, Message: fmt.Sprintf(format, args...)}
}

func skipf(c *spec.Op, msg string) spec.CheckResult {
	return spec.CheckResult{Op: c, Status: spec.StatusSkip, Message: msg}
}
