package main

import (
	"fmt"

	"github.com/opencharly/spec/spec"
)

// checkrun.go — the check-verdict result helpers + the committed-APK anchoring data.
//
// candyDirsFromScan / checkRunnerContext relocated here from check_cmd.go (K-wave 2 cone R4):
// the candy-name → SourceDir map and its carrier are the data checkrun_charly_verbs.go's
// resolveCheckApk reads off h.cc.CandyDirs()/h.cc.CandyScanErr(), and resolveCheckRunnerContext
// (check_cmd.go) still folds the same map into the check-load-plugins side effect.
//
// The check-engine driver itself is kit.Runner (sdk/kit/runner.go). The host-coupled surfaces —
// the verb dispatch (hostVerbResolver), the do-mode/context grammar (opInContext/opEffectiveContexts), and the
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
// spec.CheckRunMode selects routing rules for a check pass (W0 deleted the former in-core
// RunMode/RunModeLive/RunModeBox aliases — every consumer reads spec.CheckRunMode/
// spec.CheckModeLive/spec.CheckModeBox directly):
//
//   - spec.CheckModeLive: charly check live — against a running container. In-container
//     probes via Exec; host-side verbs (http/dns/addr) from the charly process.
//   - spec.CheckModeBox: charly check box — against a disposable container
//     (podman run --rm). All probes via Exec; host-side reachability is
//     not meaningful and those checks are skipped.

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

// candyDirsFromScan extracts the candy-name → SourceDir map from a scanned candy
// set. Keyed by the candy MAP KEY — the check's Origin form: a bare name for a
// local candy ("sshd"), the bare @github ref for a fetched one
// ("github.com/owner/repo/candy/<name>"). CollectDescriptions stamps
// Origin = "candy:" + this same key, so resolveCheckApk's CandyDirs[origin]
// lookup matches in BOTH cases. The SAME scanned map drives the plugin loader
// (R3 — one scan, both consumers).
func candyDirsFromScan(candyMap map[string]spec.CandyReader) map[string]string {
	if len(candyMap) == 0 {
		return nil
	}
	out := make(map[string]string, len(candyMap))
	for key, lyr := range candyMap {
		if lyr != nil && lyr.GetSourceDir() != "" {
			out[key] = lyr.GetSourceDir()
		}
	}
	return out
}

// checkRunnerContext carries the committed-APK anchoring (CandyDirs / CandyScanErr) a live
// baked-plan runner folds into its RunnerConfig. resolveCheckRunnerContext (check_cmd.go)
// computes it (and performs the plugin-load side effect); the caller wires the fields into
// kit.RunnerConfig.
type checkRunnerContext struct {
	CandyDirs    map[string]string
	CandyScanErr error
}
