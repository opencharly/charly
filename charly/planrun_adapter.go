package main

import (
	"context"
	"fmt"

	"github.com/opencharly/sdk/kit"
)

// planrun_adapter.go — the host seams the check-engine plan walk (kit.RunOne/RunPlan) drives
// through. The walk lives in sdk/kit and consumes the runner (kit.Runner, kit.PlanContext) plus
// two host-supplied interfaces: kit.VerbResolver (verb dispatch, satisfied by the core provider
// registry) and kit.PlanGrammar (the VerbCatalog do-mode/context grammar). The grammar the walk
// consults (VerbCatalog / opEffectiveDo / opEffectiveContexts) and the verb dispatch (the
// provider registry) STAY in core behind these seams.

// hostVerbResolver is the verb-dispatch seam — the ONE thing the walk needs from the core
// provider registry — plus the host machinery a live-container verb reaches THROUGH the check
// context: the reverse-leg endpoint/graphics/cluster/image-label resolution (check_endpoint_
// resolve.go), the out-of-process verb Invoke (provider_checkenv.go), the do:act runner
// (checkrun_act.go), and the committed-APK anchoring (checkrun_charly_verbs.go). It holds the
// kit.Runner ref (to read engine state + build the CheckContext) and OWNS the per-Invoke
// endpointCleanups (the host reverse-leg lifecycle — NOT on kit.Runner, which a plugin module
// lacks the machinery for).
type hostVerbResolver struct {
	kr *kit.Runner
	// endpointCleanups holds the ssh -L forwards opened by the ResolveEndpoint/ResolveGraphics
	// reverse-legs DURING the current verb's Invoke; invokeVerbProvider closes them AFTER the
	// Invoke returns (the forward must outlive the plugin's dial). Per-Invoke.
	endpointCleanups []func()
}

// RunVerb resolves op's verb word in the provider registry and runs it: an in-proc
// CheckVerbProvider via its typed RunVerb (threaded the host CheckContext over the kit.Runner),
// an out-of-process provider via the Invoke envelope (invokeVerbProvider). (_, false) means no
// such verb is registered — the walk reports the op as an unknown-verb skip.
func (h *hostVerbResolver) RunVerb(ctx context.Context, op *Op) (CheckResult, bool) {
	kind, err := op.Kind()
	if err != nil {
		return CheckResult{}, false
	}
	prov, ok := providerRegistry.ResolveVerb(kind)
	if !ok {
		return CheckResult{}, false
	}
	if cv, ok := prov.(CheckVerbProvider); ok {
		return cv.RunVerb(ctx, h, op), true
	}
	// An OUT-OF-PROCESS verb provider (a grpcProvider, not a CheckVerbProvider): dispatch the
	// live verb word to the Invoke envelope with the full Op — the external-charly-verb path.
	return h.invokeVerbProvider(ctx, prov, kind, op), true
}

// RunProvisionAct runs a do:act state-provision verb's create/configure act; (_, false) means
// the verb has no act path (the walk falls through to the assert dispatch).
func (h *hostVerbResolver) RunProvisionAct(ctx context.Context, op *Op, verb string) (CheckResult, bool) {
	return h.runProvisionAct(ctx, op, verb)
}

// hostPlanGrammar adapts the core VerbCatalog do-mode + execution-context grammar to
// kit.PlanGrammar. ExecContext never crosses the kit seam — InContext takes a bool (runtime vs
// build) and ContextsLabel pre-formats the effective-contexts list for the skip message.
type hostPlanGrammar struct{}

// EffectiveDo resolves op's do-mode (the keyword-stamped intentDo wins, else the verb's
// VerbCatalog default, else DoAssert).
func (hostPlanGrammar) EffectiveDo(op *Op) kit.DoMode { return opEffectiveDo(op) }

// InContext reports whether op is legal in the run's active context: runtime=true → the live
// (runtime) context, runtime=false → the box (build) context.
func (hostPlanGrammar) InContext(op *Op, runtime bool) bool {
	wantCtx := CtxBuild
	if runtime {
		wantCtx = CtxRuntime
	}
	return opInContext(op, wantCtx)
}

// ContextsLabel is op's effective-contexts list pre-formatted for the context-skip message —
// the SAME %v rendering the former core ContextSkipReason used, so the message is
// byte-identical.
func (hostPlanGrammar) ContextsLabel(op *Op) string {
	return fmt.Sprintf("%v", opEffectiveContexts(op))
}

// venueResolver adapts the core liveTargetResolver (an `on:` DRIVER venue → *CheckVarResolver +
// DeployExecutor) to the kit.VenueResolver seam (venue → kit.Executor + env + hasRuntime) the
// runner's per-step SwapVenue drives. It always returns a non-nil env map so SwapVenue swaps the
// engine env for the driver venue (matching the former "always swap the resolver" behaviour: a
// driver with no resolvable vars clears the base env rather than leaking the subject's).
func venueResolver(instance string) kit.VenueResolver {
	resolve := liveTargetResolver(instance)
	return func(venue string) (kit.Executor, map[string]string, bool, error) {
		res, exec, err := resolve(venue)
		if err != nil {
			return nil, nil, false, err
		}
		env := map[string]string{}
		hasRuntime := false
		if res != nil {
			if res.Env != nil {
				env = res.Env
			}
			hasRuntime = res.HasRuntime
		}
		return exec, env, hasRuntime, nil
	}
}
