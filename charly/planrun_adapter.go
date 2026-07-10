package main

import (
	"context"
	"fmt"
	"time"

	"github.com/opencharly/sdk/kit"
)

// planrun_adapter.go — the host side of the check-engine plan walk, dissolved into sdk/kit
// (kit/planrun.go). The walk drives the host through kit.PlanContext + kit.VerbResolver;
// these wrappers adapt the live *Runner to those interfaces. They are wrapper structs (NOT
// methods on *Runner) because *Runner already has fields named Distros/Mode/VerifyOnly/
// SkipDeterministicRun/Scenario/Grader — a method of the same name would collide (the SAME
// reason runnerCheckContext wraps rather than extends). The grammar the walk consults
// (VerbCatalog / opEffectiveDo / opEffectiveContexts) and the verb dispatch (the provider
// registry) STAY in core behind these seams.

// runnerPlanContext adapts *Runner to kit.PlanContext — the driver surface the plan walk reads.
type runnerPlanContext struct{ r *Runner }

func (c runnerPlanContext) Distros() []string          { return c.r.Distros }
func (c runnerPlanContext) VerifyOnly() bool           { return c.r.VerifyOnly }
func (c runnerPlanContext) SkipDeterministicRun() bool { return c.r.SkipDeterministicRun }
func (c runnerPlanContext) EffectiveEnv() map[string]string {
	return c.r.effectiveEnv()
}
func (c runnerPlanContext) ProbeNeverHang(op *Op) time.Duration { return c.r.probeNeverHang(op) }
func (c runnerPlanContext) EffectiveDo(op *Op) kit.DoMode       { return opEffectiveDo(op) }
func (c runnerPlanContext) Scenario() *ScenarioContext          { return c.r.Scenario }
func (c runnerPlanContext) SetScenario(sc *ScenarioContext)     { c.r.Scenario = sc }
func (c runnerPlanContext) Verbs() kit.VerbResolver             { return runnerVerbResolver(c) }
func (c runnerPlanContext) Grader() kit.StepGrader              { return c.r.Grader }

func (c runnerPlanContext) Mode() kit.RunMode {
	if c.r.Mode == RunModeBox {
		return kit.ModeBox
	}
	return kit.ModeLive
}

// ContextSkipReason wraps the core VerbCatalog grammar: it returns a non-empty skip message
// when the op's effective execution context is not active in the run's mode (box→build,
// live→runtime). ExecContext never crosses the kit seam — the message is pre-formatted here.
func (c runnerPlanContext) ContextSkipReason(op *Op) string {
	wantCtx := CtxRuntime
	modeName := "live"
	if c.r.Mode == RunModeBox {
		wantCtx, modeName = CtxBuild, "box"
	}
	if !opInContext(op, wantCtx) {
		return fmt.Sprintf("context %v not active in %s mode", opEffectiveContexts(op), modeName)
	}
	return ""
}

// SwapVenue retargets the executor + resolver + image to op's per-step venue for the duration
// of one dispatch, returning a restore func (nil when no swap) and a non-empty failReason when
// the venue cannot be resolved. It mutates the *Runner in place so EffectiveEnv + the verb
// dispatch (which read r.Exec/r.Resolver/r.Box) see the swapped venue — the same self-swap
// guard the classical inline path used (venue set, differs from the active target, and a
// TargetResolver is wired).
func (c runnerPlanContext) SwapVenue(op *Op) (func(), string) {
	r := c.r
	if op.Venue == "" || op.Venue == r.Box || r.TargetResolver == nil {
		return nil, ""
	}
	newResolver, newExec, terr := r.TargetResolver(op.Venue)
	if terr != nil {
		return nil, fmt.Sprintf("venue %q — %v", op.Venue, terr)
	}
	origExec, origResolver, origImage := r.Exec, r.Resolver, r.Box
	if newExec != nil {
		r.Exec = newExec
	}
	if newResolver != nil {
		r.Resolver = newResolver
	}
	r.Box = op.Venue
	return func() {
		r.Exec = origExec
		r.Resolver = origResolver
		r.Box = origImage
	}, ""
}

// runnerVerbResolver adapts *Runner to kit.VerbResolver — the verb-dispatch seam. The walk
// hands it an already-variable-expanded, already-validated Op; this resolves the verb word in
// the provider registry and runs it (in-proc CheckVerbProvider fast path, else the
// out-of-process Invoke envelope), or reports the provision-act for a do:act state verb.
type runnerVerbResolver struct{ r *Runner }

func (v runnerVerbResolver) RunVerb(ctx context.Context, op *Op) (CheckResult, bool) {
	kind, err := op.Kind()
	if err != nil {
		return CheckResult{}, false
	}
	prov, ok := providerRegistry.ResolveVerb(kind)
	if !ok {
		return CheckResult{}, false
	}
	if cv, ok := prov.(CheckVerbProvider); ok {
		return cv.RunVerb(ctx, v.r, op), true
	}
	// An OUT-OF-PROCESS verb provider (a grpcProvider, not a CheckVerbProvider): dispatch the
	// live verb word to the Invoke envelope with the full Op — the external-charly-verb path.
	return v.r.invokeVerbProvider(ctx, prov, kind, op), true
}

func (v runnerVerbResolver) RunProvisionAct(ctx context.Context, op *Op, verb string) (CheckResult, bool) {
	return v.r.runProvisionAct(ctx, op, verb)
}
