package main

import (
	"context"
	"net/http"
	"slices"
	"time"

	"github.com/opencharly/spec/spec"
)

// hostCheckCarrier is the spec-backed carrier that backs a hostVerbResolver's live CheckContext
// legs (Exec/Mode/HTTPDo/Box/Instance/Distros/DialTimeout/AddBackground) WITHOUT core referencing
// kit.Runner (#55 CHECK-ENGINE cone — the reverse-channel rendezvous no longer imports the engine).
// It carries the CheckEnv scalars plus a LIVE exec getter (SwapVenue-aware: a live plan drive
// captures the running kit.Runner so a cross-deployment `on:`/${HOST:member} step's mid-plan venue
// swap is reflected — a FROZEN executor was the check-k3s-vm cross-deployment SIGSEGV class) + the
// host HTTP base client + a nil-safe background-PID sink. In PRODUCTION the reverse-channel path
// (plugin_dispatch_reverse.go — from the wire CheckEnv snapshot, fixed venue) is now the SOLE
// producer: the in-proc plan drive that once also filled it (checkrun.go newCheckRunner, projected
// off a live kit.Runner) moved PLUGIN-SIDE (command:check OpVerifyChecks, #55 CHECK-ENGINE cone Unit
// 2) and survives only as the test-only carrierFromRunner (checkrun_helpers_test.go). Zero kit in
// the carrier itself.
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

// opEffectiveContexts returns the op's resolved execution contexts: an explicit
// Context wins, else the verb's VerbCatalog default, else nil. The do-mode/context
// grammar the plan walk consults — relocated from checkspec.go (K-wave 2 cone R4) to
// sit beside hostVerbResolver, the other half of this grammar seam (see below).
func opEffectiveContexts(c *spec.Op) []spec.ExecContext {
	if len(c.Context) > 0 {
		out := make([]spec.ExecContext, 0, len(c.Context))
		for _, s := range c.Context {
			out = append(out, spec.ExecContext(s))
		}
		return out
	}
	if verb, err := c.Kind(); err == nil {
		if vs, ok := spec.VerbCatalog[verb]; ok {
			return vs.Contexts
		}
	}
	return nil
}

// opInContext reports whether the op is legal in ctx per its effective contexts. Its
// spec.OpInContext DI-hook registration lives in layers.go (which already imports spec).
func opInContext(c *spec.Op, ctx spec.ExecContext) bool {
	return slices.Contains(opEffectiveContexts(c), ctx)
}

// planrun_adapter.go — the host seams the check-engine plan walk (kit.RunOne/RunPlan) drives
// through. The walk lives in sdk/kit and consumes the runner (kit.Runner, kit.PlanContext) plus
// two host-supplied interfaces: kit.VerbResolver (verb dispatch, satisfied by the core provider
// registry) and kit.PlanGrammar (the VerbCatalog do-mode/context grammar). The grammar the walk
// consults (VerbCatalog / opEffectiveContexts) and the verb dispatch (the
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
	// A COMPILED-IN plugin candy whose pb.ProviderServer ALSO implements the typed
	// spec.CheckVerbProvider contract (a command+verb candy like agentteams): dispatch the
	// verb in-proc with the live host CheckContext — the SAME adaptation kitVerbAdapter
	// performs for a kit-shape candy, so a compiled-in verb needing the executor
	// (ResolveEndpoint/Exec) gets it without a broker (the mcp pattern's sdk.NewCheckContext
	// is out-of-process-only).
	if ip, ok := prov.(*inprocProvider); ok {
		if kv, ok := ip.srv.(spec.CheckVerbProvider); ok {
			res := kv.RunVerb(ctx, hostCheckContext{h: h}, op)
			return spec.CheckResult{Op: op, Verb: kv.Reserved(), Status: res.Status, Message: res.Message}, true
		}
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

// The former venueResolver — the `on:` DRIVER venue → kit.VenueResolver adapter for the IN-PROC
// plan drive — is DELETED (#55 CHECK-ENGINE cone Unit 2): the deploy-scope check DRIVE moved
// PLUGIN-SIDE (command:check OpVerifyChecks), where candy/plugin-check's pluginVenueResolver +
// members.go rebuild the cross-deployment resolution over the check reverse channel. The core
// liveTargetResolver/resolveHostVars cluster it wrapped (charly/check_members.go) went dead with it
// and was removed in the same cutover.
