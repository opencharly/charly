package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"

	"github.com/opencharly/sdk/kit"
)

// checkrun.go — check-runner CONSTRUCTION + the package-main result-model bindings.
//
// The check-engine driver itself is kit.Runner (sdk/kit/runner.go, relocated from core in
// P12): it implements kit.PlanContext and carries the shared engine state, so any plugin
// candy that runs a plan drives the SAME loop. The host-coupled surfaces stay in charly core
// behind the injected seams built here — the verb dispatch (hostVerbResolver), the do-mode/
// context grammar (hostPlanGrammar), and the per-step venue swap (venueResolver) — plus the
// live-verb CheckContext (hostCheckContext) wrapping the runner. newCheckRunner wires them.
//
// CheckStatus, CheckResult, and the pass/fail/skip verdict constants live ONCE in sdk/kit —
// they are the check engine's result model, shared with every plugin candy that runs a plan.
// These are the package-main bindings; core's call sites are unchanged.
//
// kit.Status is the single pass/fail/skip enum: a verb's kit.Result verdict flows into a
// CheckResult with no conversion.
type CheckStatus = kit.Status

const (
	TestPass = kit.StatusPass
	TestFail = kit.StatusFail
	TestSkip = kit.StatusSkip
)

// CheckResult is the engine's per-step result record; StepResult wraps it with the step's
// identity. Both live in kit (checkresult.go).
type CheckResult = kit.CheckResult

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
// check runner shares: the verb dispatch (hostVerbResolver — which holds the runner ref and
// the per-Invoke host endpoint cleanups), the do-mode/context grammar (hostPlanGrammar), and
// the per-probe never-hang floor (the readiness-config PerAttemptFor(PollLocal) value the core
// check runner has always used). The caller fills cfg with the per-site fields (Exec/Mode/Env/
// Box/… and, for a live cross-deployment pass, TargetResolver + HostVars). Verbs/Grammar/
// ProbeTimeout it sets here are always overridden — a caller never wires them.
func newCheckRunner(cfg kit.RunnerConfig) *kit.Runner {
	hvr := &hostVerbResolver{}
	cfg.Verbs = hvr
	cfg.Grammar = hostPlanGrammar{}
	if cfg.ProbeTimeout == 0 {
		cfg.ProbeTimeout = loadedReadiness().PerAttemptFor(PollLocal)
	}
	kr := kit.NewRunner(cfg)
	hvr.kr = kr
	return kr
}

// newHostVerbResolver wraps a kit.Runner in the host verb resolver — the verb-dispatch seam
// plus the reverse-leg host machinery (endpoint/graphics/cluster/image-label resolution,
// out-of-process verb Invoke) and the per-Invoke endpoint cleanups. newCheckRunner builds one
// internally; this constructor is the direct entry a compiled-in kit verb's RunVerb needs
// (the host CheckContext source) when dispatched outside a full runner build.
func newHostVerbResolver(kr *kit.Runner) *hostVerbResolver {
	return &hostVerbResolver{kr: kr}
}

// deployExecOf recovers the concrete DeployExecutor a kit.Runner was built with. The runner
// stores its venue executor as the narrow kit.Executor (kit cannot import DeployExecutor), but
// every check runner is constructed with a DeployExecutor, so the widening assertion succeeds;
// a nil/absent exec yields nil. Used by the host verb dispatch, which needs the full
// DeployExecutor surface (Venue/PutFile/GetFile) the reverse channel serves.
func deployExecOf(kr *kit.Runner) deploykit.DeployExecutor {
	if e, ok := kr.Exec().(deploykit.DeployExecutor); ok {
		return e
	}
	return nil
}

// resolverEnv projects a *kit.CheckVarResolver into the kit.RunnerConfig Env + HasRuntime pair
// (nil-safe — a nil resolver yields no env, no runtime state).
func resolverEnv(res *kit.CheckVarResolver) (map[string]string, bool) {
	if res == nil {
		return nil, false
	}
	return res.Env, res.HasRuntime
}

// currentCharlyExecutable is the executable that owns this check run. Keeping it
// injectable lets the resolver contract prove that host-side plan re-entry uses
// the active binary rather than whatever a stale PATH happens to select.
var currentCharlyExecutable = os.Executable

// stampCharlyBin records the active charly executable path into a runtime check-var
// resolver's Env as CHARLY_BIN, so host-side R10 plan re-entry (a plan step referencing
// ${CHARLY_BIN}) drives the active binary instead of a stale PATH selection. CHARLY_BIN
// is deliberately never synthesized from PATH: an unavailable executable leaves the
// variable unresolved instead of silently selecting an unrelated installed Charly.
// nil-safe; idempotent.
func stampCharlyBin(res *kit.CheckVarResolver) *kit.CheckVarResolver {
	if res == nil {
		return res
	}
	if res.Env == nil {
		res.Env = map[string]string{}
	}
	if path, err := currentCharlyExecutable(); err == nil && strings.TrimSpace(path) != "" {
		res.Env["CHARLY_BIN"] = path
	}
	return res
}

// newRuntimeCheckVarResolver constructs a runtime check-var resolver (HasRuntime
// true) from an env map, stamping CHARLY_BIN via stampCharlyBin. The
// direct-construction analogue of the kit.ResolveCheckVarsRuntime call sites
// (which stampCharlyBin their result).
func newRuntimeCheckVarResolver(env map[string]string) *kit.CheckVarResolver {
	if env == nil {
		env = map[string]string{}
	}
	return stampCharlyBin(&kit.CheckVarResolver{Env: env, HasRuntime: true})
}

// ---------------------------------------------------------------------------
// Result helpers
// ---------------------------------------------------------------------------

func passf(c *spec.Op, msg string) CheckResult {
	return CheckResult{Op: c, Status: TestPass, Message: msg}
}

func failf(c *spec.Op, format string, args ...any) CheckResult {
	return CheckResult{Op: c, Status: TestFail, Message: fmt.Sprintf(format, args...)}
}

func skipf(c *spec.Op, msg string) CheckResult {
	return CheckResult{Op: c, Status: TestSkip, Message: msg}
}
