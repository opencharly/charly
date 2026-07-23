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
// live venue executor, its serializable descriptor (for InvokeProvider's S1 seam — nil when the
// venue is a shape that doesn't round-trip, e.g. a genuine multi-hop composition; the runner
// still works, an out-of-process verb dispatch just falls back to InvokeProvider's own default
// "thread the caller's incoming executor" behavior), and the env snapshot every verb dispatch
// carries. Mirrors charly/checkrun.go's newCheckRunner: the caller fills cfg with the per-site
// fields (Exec/Mode/Env/Box/... and, for a live cross-deployment pass, TargetResolver/HostVars);
// Verbs/Grammar/ProbeTimeout are always set here, never by the caller.
func newPluginCheckRunner(ex *sdk.Executor, ctx context.Context, env spec.CheckEnv, venueDesc *spec.VenueDescriptor, cfg kit.RunnerConfig) *kit.Runner {
	cfg.Verbs = &pluginVerbResolver{ex: ex, ctx: ctx, env: env, venueDesc: venueDesc}
	cfg.Grammar = pluginPlanGrammar{}
	if cfg.ProbeTimeout == 0 {
		cfg.ProbeTimeout = kit.ReadinessProvider().PerAttemptFor(vmshared.PollLocal)
	}
	return kit.NewRunner(cfg)
}
