package main

import (
	"github.com/opencharly/spec/spec"

	"github.com/opencharly/sdk/kit"
)

// checkrun_helpers_test.go — the IN-PROC check-runner construction helpers, now TEST-ONLY.
//
// newCheckRunner + carrierFromRunner used to live in production checkrun.go: they wired a kit.Runner
// with the host seams (hostVerbResolver dispatch, hostPlanGrammar, the readiness ProbeTimeout) and
// projected the runner into the hostCheckCarrier the CheckContext legs read. In #55 CHECK-ENGINE
// cone Unit 2 the deploy-scope check DRIVE moved PLUGIN-SIDE (command:check OpVerifyChecks —
// candy/plugin-check builds the runner now), so production charly core no longer constructs a
// kit.Runner and checkrun.go dropped its sdk/kit import. These two constructors survive here because
// their ONLY remaining consumers are the check-engine unit tests (checkrun_test.go / plan_unify_test.go
// / poll_probe_neverhang_test.go / apk_format_test.go / check_members_test.go / check_helpers_test.go),
// which exercise the STILL-LIVE hostVerbResolver→registry→builtin-verb dispatch surface (the SAME
// surface plugin_dispatch_reverse.go builds for the reverse channel from the wire CheckEnv snapshot).
//
// Test-fidelity note (#55 Unit 2, orchestrator-flagged): carrierFromRunner projects the carrier from
// a LIVE kit.Runner, whereas production's reverse channel (plugin_dispatch_reverse.go) fills the SAME
// *hostCheckCarrier shape from the CheckEnv wire snapshot. The two constructions populate the
// identical carrier fields and drive the identical dispatch, so the tests remain faithful to the live
// surface; only the from-a-live-runner projection path is now test-only.

// newCheckRunner builds a kit.Runner for a check pass, wiring the standard host seams (the verb
// dispatch hostVerbResolver + its spec-backed carrier, the do-mode/context grammar hostPlanGrammar,
// and the readiness-config per-probe never-hang floor). The caller fills cfg with the per-site fields
// (Exec/Mode/Env/Box/… and, for a live cross-deployment pass, TargetResolver + HostVars).
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
// hostVerbResolver's CheckContext legs. execFn + addBg capture the runner LIVE (a mid-plan SwapVenue
// is reflected — the reason exec is a getter, not a frozen value); the scalars snapshot the static
// engine state.
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
