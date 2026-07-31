package main

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/opencharly/spec/spec"

	"github.com/opencharly/sdk/kit"
)

// hostCheckCarrier is the spec-backed carrier that backs a hostVerbResolver's live CheckContext
// legs (Exec/Mode/HTTPDo/Box/Instance/Distros/DialTimeout/AddBackground) WITHOUT core referencing
// kit.Runner (#55 CHECK-ENGINE cone — the reverse-channel rendezvous no longer imports the engine).
// It carries the CheckEnv scalars plus a LIVE exec getter (SwapVenue-aware: the in-proc plan drive
// captures the running kit.Runner so a cross-deployment `on:`/${HOST:member} step's mid-plan venue
// swap is reflected — a FROZEN executor was the check-k3s-vm cross-deployment SIGSEGV class) + the
// host HTTP base client + a nil-safe background-PID sink. Two producers fill it: the reverse-channel
// path (plugin_dispatch_reverse.go — from the wire CheckEnv snapshot, fixed venue) and the in-proc
// plan drive (checkrun.go newCheckRunner — projected off the kit.Runner it still builds as its
// engine). Zero kit in the carrier itself.
type hostCheckCarrier struct {
	execFn       func() spec.DeployExecutor // live getter (SwapVenue-aware); accessed via Exec()
	mode         spec.CheckRunMode
	box          string
	vmName       string
	instance     string
	distros      []string
	dialTimeout  time.Duration
	httpBase     *http.Client
	addBg        func(pid int) // nil when there is no scenario context (a no-op AddBackground)
	candyDirs    map[string]string
	candyScanErr error
}

// The accessors mirror the kit.Runner method names the check dispatch reads, so the reverse-channel
// + host-verb-resolver call sites are a clean field-name swap (kr→cc), not a re-shape.
func (c *hostCheckCarrier) Exec() spec.DeployExecutor { // live (SwapVenue-aware), nil-safe
	if c == nil || c.execFn == nil {
		return nil
	}
	return c.execFn()
}
func (c *hostCheckCarrier) Mode() spec.CheckRunMode      { return c.mode }
func (c *hostCheckCarrier) Box() string                  { return c.box }
func (c *hostCheckCarrier) Instance() string             { return c.instance }
func (c *hostCheckCarrier) Distros() []string            { return c.distros }
func (c *hostCheckCarrier) DialTimeout() time.Duration   { return c.dialTimeout }
func (c *hostCheckCarrier) HTTPClient() *http.Client     { return c.httpBase }
func (c *hostCheckCarrier) CandyDirs() map[string]string { return c.candyDirs }
func (c *hostCheckCarrier) CandyScanErr() error          { return c.candyScanErr }

// AddBg registers a host-side background PID (nil-safe: a no-op when there is no scenario context).
func (c *hostCheckCarrier) AddBg(pid int) {
	if c != nil && c.addBg != nil {
		c.addBg(pid)
	}
}

// VmTargetName mirrors kit.Runner.VmTargetName: the VM domain-target (vmName, else box) the host
// vm/spice/libvirt legs hand the plugin.
func (c *hostCheckCarrier) VmTargetName() string {
	if c.vmName != "" {
		return c.vmName
	}
	return c.box
}

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
// spec-backed hostCheckCarrier (the CheckContext engine-state legs, projected off the live venue —
// NOT kit.Runner, so this rendezvous imports only spec) and OWNS the per-Invoke endpointCleanups
// (the host reverse-leg lifecycle — which a plugin module lacks the machinery for).
type hostVerbResolver struct {
	cc *hostCheckCarrier
	// endpointCleanups holds the ssh -L forwards opened by the ResolveEndpoint/ResolveGraphics
	// reverse-legs DURING the current verb's Invoke; invokeVerbProvider closes them AFTER the
	// Invoke returns (the forward must outlive the plugin's dial). Per-Invoke.
	endpointCleanups []func()
}

// RunVerb resolves op's verb word in the provider registry and runs it: an in-proc
// CheckVerbProvider via its typed RunVerb (threaded the host CheckContext over the kit.Runner),
// an out-of-process provider via the Invoke envelope (invokeVerbProvider). (_, false) means no
// such verb is registered — the walk reports the op as an unknown-verb skip.
func (h *hostVerbResolver) RunVerb(ctx context.Context, op *spec.Op) (spec.CheckResult, bool) {
	kind, err := op.Kind()
	if err != nil {
		return spec.CheckResult{}, false
	}
	prov, ok := providerRegistry.ResolveVerb(kind)
	if !ok {
		return spec.CheckResult{}, false
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
func (h *hostVerbResolver) RunProvisionAct(ctx context.Context, op *spec.Op, verb string) (spec.CheckResult, bool) {
	return h.runProvisionAct(ctx, op, verb)
}

// hostPlanGrammar adapts the core VerbCatalog do-mode + execution-context grammar to
// kit.PlanGrammar. ExecContext never crosses the kit seam — InContext takes a bool (runtime vs
// build) and ContextsLabel pre-formats the effective-contexts list for the skip message.
type hostPlanGrammar struct{}

// EffectiveDo resolves op's do-mode (the keyword-stamped intentDo wins, else the verb's
// VerbCatalog default, else DoAssert).
func (hostPlanGrammar) EffectiveDo(op *spec.Op) spec.DoMode { return opEffectiveDo(op) }

// InContext reports whether op is legal in the run's active context: runtime=true → the live
// (runtime) context, runtime=false → the box (build) context.
func (hostPlanGrammar) InContext(op *spec.Op, runtime bool) bool {
	wantCtx := spec.CtxBuild
	if runtime {
		wantCtx = spec.CtxRuntime
	}
	return opInContext(op, wantCtx)
}

// ContextsLabel is op's effective-contexts list pre-formatted for the context-skip message —
// the SAME %v rendering the former core ContextSkipReason used, so the message is
// byte-identical.
func (hostPlanGrammar) ContextsLabel(op *spec.Op) string {
	return fmt.Sprintf("%v", opEffectiveContexts(op))
}

// venueResolver adapts the core liveTargetResolver (an `on:` DRIVER venue → *kit.CheckVarResolver +
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
