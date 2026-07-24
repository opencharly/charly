package check

// plugin_runner.go — K1-unblock W3 Unit B: newPluginCheckRunner, the plugin-side counterpart of
// charly/checkrun.go's newCheckRunner. Wires the SAME three seams every check runner needs
// (Verbs/Grammar/ProbeTimeout) using this file's own portable implementations instead of core's
// hostVerbResolver/hostPlanGrammar/loadedReadiness — confirmed byte-identical behavior for
// Grammar (plan_grammar.go, ported verbatim) and ProbeTimeout (kit.ReadinessProvider() is the
// SAME resolver loadedReadiness() wraps, already threaded to this plugin process via
// CHARLY_READINESS_* env at Connect — see venue.go's header). Verbs is the new mechanism
// (verb_resolver.go's pluginVerbResolver), proven by the W3 Unit B spike.

import (
	"context"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
	"github.com/opencharly/sdk/vmshared"
)

// newPluginCheckRunner builds a kit.Runner for a check pass driven from this plugin, given the
// env snapshot every out-of-process verb dispatch carries. Mirrors charly/checkrun.go's
// newCheckRunner: the caller fills cfg with the per-site fields (Exec/Mode/Env/Box/... and, for a
// live cross-deployment pass, TargetResolver/HostVars); Verbs/Grammar/ProbeTimeout are always set
// here, never by the caller.
//
// The VerbResolver holds a BACK-REFERENCE to the constructed *kit.Runner (pvr.kr = kr, mirroring
// charly/checkrun.go's hvr.kr = kr) so an out-of-process verb dispatch (InvokeProvider's S1
// VenueDescriptor seam) always threads the runner's CURRENT executor — including one SwapVenue
// retargeted mid-plan for a cross-deployment (`on:`/${HOST:member}) or GROUP-member step, never a
// venue frozen at construction time. RCA'd live (check-k3s-vm SIGSEGV'd on a nil cc.Exec() inside
// a `command:` step on a VM target): the former static-venueDesc-at-construction design left
// InvokeProvider's fallback ("thread the caller's incoming s.exec") in play whenever the runner's
// OWN default venue never round-tripped — which was ALWAYS true for a plan's first/default step
// dispatched from a top-level `charly check ...` command Invoke (no ambient deploy-context s.exec
// of its own). Deriving the descriptor fresh from r.kr.Exec() on every RunVerb call fixes both
// that default-venue gap AND the SwapVenue-tracking gap in one generic mechanism.
func newPluginCheckRunner(ex *sdk.Executor, ctx context.Context, env spec.CheckEnv, cfg kit.RunnerConfig) *kit.Runner {
	pvr := &pluginVerbResolver{ex: ex, ctx: ctx, env: env}
	cfg.Verbs = pvr
	cfg.Grammar = pluginPlanGrammar{}
	if cfg.ProbeTimeout == 0 {
		cfg.ProbeTimeout = kit.ReadinessProvider().PerAttemptFor(vmshared.PollLocal)
	}
	kr := kit.NewRunner(cfg)
	pvr.kr = kr
	return kr
}
