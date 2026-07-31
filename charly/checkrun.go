package main

import (
	"fmt"

	"github.com/opencharly/spec/spec"

	"github.com/opencharly/sdk/kit"
)

// checkrun.go — check-runner CONSTRUCTION + the package-main result-model bindings.
//
// The check-engine driver itself is kit.Runner (sdk/kit/runner.go, relocated from core in
// P12): it implements kit.PlanContext and carries the shared engine state, so any plugin
// candy that runs a plan drives the SAME loop. The host-coupled surfaces stay in charly core
// behind the injected seams built here — the verb dispatch (hostVerbResolver), the do-mode/
// context grammar (hostPlanGrammar), and the per-step venue swap (venueResolver) — plus the
// live-verb CheckContext (hostCheckContext) over the spec-backed hostCheckCarrier the runner
// projects into (carrierFromRunner), so the rendezvous reads engine state without importing
// kit.Runner. newCheckRunner wires them.
//
// FLOOR-SLIM Unit 4: the former package-main CheckStatus/CheckResult/TestPass/TestFail/TestSkip
// aliases are DELETED. spec.CheckResult (CUE-sourced, sdk/schema/checkresult.cue) is the
// verdict envelope every registry-coupled floor file (provider.go/provider_verb.go/
// verb_builtins.go/unified_targets.go/provider_checkenv.go, plus this file's passf/failf/skipf)
// now references DIRECTLY — zero new sdk/kit import. sdk/kit.CheckResult (the engine's richer
// internal type, embedding spec.CheckResult + the engine-internal DeadlineExceeded retry
// signal that never crosses the wire) is used only inside sdk/kit + candy/plugin-check, which
// already import kit. spec.StatusPass/StatusFail/StatusSkip are the verdict constants.
//
// RunMode selects routing rules for a check pass. It is a package-main binding onto kit.RunMode
// (relocated with the runner); RunModeLive/RunModeBox map to kit.ModeLive/kit.ModeBox.
//
//   - RunModeLive: charly check live — against a running container. In-container
//     probes via Exec; host-side verbs (http/dns/addr) from the charly process.
//   - RunModeBox: charly check box — against a disposable container
//     (podman run --rm). All probes via Exec; host-side reachability is
//     not meaningful and those checks are skipped.
type RunMode = kit.RunMode

const (
	RunModeLive = kit.ModeLive
	RunModeBox  = kit.ModeBox
)

// newCheckRunner builds a kit.Runner for a check pass, wiring the standard host seams every
// check runner shares: the verb dispatch (hostVerbResolver — which holds the spec-backed carrier
// projected off the runner + the per-Invoke host endpoint cleanups), the do-mode/context grammar
// (hostPlanGrammar), and
// the per-probe never-hang floor (the readiness-config PerAttemptFor(spec.PollLocal) value the core
// check runner has always used). The caller fills cfg with the per-site fields (Exec/Mode/Env/
// Box/… and, for a live cross-deployment pass, TargetResolver + HostVars). Verbs/Grammar/
// ProbeTimeout it sets here are always overridden — a caller never wires them.
func newCheckRunner(cfg kit.RunnerConfig) *kit.Runner {
	hvr := &hostVerbResolver{}
	cfg.Verbs = hvr
	cfg.Grammar = hostPlanGrammar{}
	if cfg.ProbeTimeout == 0 {
		cfg.ProbeTimeout = loadedReadiness().PerAttemptFor(spec.PollLocal)
	}
	kr := kit.NewRunner(cfg)
	hvr.cc = carrierFromRunner(kr)
	return kr
}

// carrierFromRunner projects a live kit.Runner into the hostCheckCarrier that backs a
// hostVerbResolver's CheckContext legs — the in-proc plan-drive producer (newCheckRunner + the
// unit-test helpers). execFn + addBg capture the runner LIVE (a mid-plan SwapVenue / a late
// SetScenario is reflected — the reason exec is a getter, not a frozen value); the scalars
// snapshot the static engine state. The narrow kit.Executor widens to spec.DeployExecutor (every
// check runner is built with one; a nil/absent exec yields nil).
func carrierFromRunner(kr *kit.Runner) *hostCheckCarrier {
	return &hostCheckCarrier{
		execFn: func() spec.DeployExecutor {
			if e, ok := kr.Exec().(spec.DeployExecutor); ok {
				return e
			}
			return nil
		},
		mode:        kr.Mode(),
		box:         kr.Box(),
		vmName:      kr.VmName(),
		instance:    kr.Instance(),
		distros:     kr.Distros(),
		dialTimeout: kr.DialTimeout(),
		httpBase:    kr.HTTPClient(),
		addBg: func(pid int) {
			if s := kr.Scenario(); s != nil {
				s.AddBackground(pid)
			}
		},
		candyDirs:    kr.CandyDirs(),
		candyScanErr: kr.CandyScanErr(),
	}
}

// resolverEnv projects a *kit.CheckVarResolver into the kit.RunnerConfig Env + HasRuntime pair
// (nil-safe — a nil resolver yields no env, no runtime state).
func resolverEnv(res *kit.CheckVarResolver) (map[string]string, bool) {
	if res == nil {
		return nil, false
	}
	return res.Env, res.HasRuntime
}

// CHARLY_BIN stamping (StampCharlyBin / NewRuntimeCheckVarResolver / CurrentCharlyExecutable)
// moved to sdk/kit (checkvars_charlybin.go, CHECK-cone move) — every dependency was already
// portable (os.Executable, strings.TrimSpace, kit.CheckVarResolver), and both check
// (check_members.go) and deploy (check_cmd.go's local --verify path) call sites need it, so
// it lives in the shared kit rather than either cone's plugin (R3).

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
