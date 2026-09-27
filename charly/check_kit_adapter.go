package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/opencharly/spec/checkstep"
	"github.com/opencharly/spec/http"
	"github.com/opencharly/spec/ops"
	"github.com/opencharly/spec/spec"

	pb "github.com/opencharly/spec/proto"
)

// hostCheckContext adapts the live check pass to spec.CheckContext — the surface a HOST-COUPLED
// verb candy consumes. It wraps the *hostVerbResolver (which holds the kit.Runner engine state +
// the per-Invoke endpoint cleanups) rather than the runner directly, so the six host-reverse legs
// (HTTPDo + the four Resolve* podman/go-libvirt/ssh ops) share ONE cleanup lifecycle with the
// out-of-process dispatch. The engine-state legs read the kit.Runner accessors; the host-reverse
// legs (check_endpoint_resolve.go) call the SAME resolveVerb* machinery the out-of-proc
// checkContextReverseServer uses — one source, two consumers, endpoint-identical (R3).
// DeployExecutor satisfies spec.CheckExecutor structurally, so Exec() returns the runner's straight
// through.
type hostCheckContext struct{ h *hostVerbResolver }

var _ spec.CheckContext = hostCheckContext{}

func (c hostCheckContext) Exec() spec.CheckExecutor   { return c.h.cc.Exec() }
func (c hostCheckContext) DialTimeout() time.Duration { return c.h.cc.DialTimeout() }

// HTTPDo issues the request from the host (in-process) via the SHARED host HTTP-do path
// (spec.DoHTTPRequest — the SAME builder the out-of-process CheckContextService.HTTPDo uses, R3),
// derived from the carrier's base client.
func (c hostCheckContext) HTTPDo(ctx context.Context, req spec.CheckHTTPRequest) (spec.CheckHTTPResponse, error) {
	return http.DoHTTPRequest(ctx, c.h.cc.HTTPClient(), req)
}
func (c hostCheckContext) Box() string           { return c.h.cc.Box() }
func (c hostCheckContext) Instance() string      { return c.h.cc.Instance() }
func (c hostCheckContext) Distros() []string     { return c.h.cc.Distros() }
func (c hostCheckContext) AddBackground(pid int) { c.h.cc.AddBg(pid) }

// InvokeProvider dispatches a provider invocation in-proc through the SAME registry
// the out-of-process reverse server uses (providerRegistry.resolve + Provider.Invoke) -
// the in-proc half of the one-dial contract (spec#97): session/service verbs reach the
// runner registry without a second broker dial.
func (c hostCheckContext) InvokeProvider(ctx context.Context, class, word, op string, paramsJSON, env []byte) ([]byte, error) {
	prov, ok := providerRegistry.resolve(ProviderClass(class), word)
	if !ok {
		return nil, fmt.Errorf("check context: no provider for %s:%s", class, word)
	}
	res, err := prov.Invoke(ctx, &Operation{Reserved: word, Op: op, Params: paramsJSON, Env: env})
	if err != nil {
		return nil, fmt.Errorf("check context: invoke %s:%s: %w", class, word, err)
	}
	if res == nil {
		return nil, fmt.Errorf("check context: invoke %s:%s returned no result", class, word)
	}
	return res.JSON, nil
}
func (c hostCheckContext) Mode() spec.CheckRunMode { return c.h.cc.Mode() }

// kitVerbAdapter wraps a COMPILED-IN host-coupled verb candy's checkstep.CheckVerbProvider
// as a package-main CheckVerbProvider, so runOne dispatches it through the SAME
// providerRegistry path as an typed builtin verb. It passes the live check context
// (hostCheckContext over the *hostVerbResolver) as a spec.CheckContext and converts the
// returned kit.Result back to a CheckResult (stamping Op + Verb). It embeds
// builtinVerbBase for Class()=ClassVerb + the in-proc-only Invoke stub — a kit verb is
// in-process only (RunVerb needs the live host context, which cannot cross a process
// boundary).
type kitVerbAdapter struct {
	builtinVerbBase
	kv spec.CheckVerbProvider
	// primary is the capability's declared scalar-sugar primary input field
	// (ProvidedCapability.Primary), lifted from Describe at registration.
	primary string
}

func (a kitVerbAdapter) Reserved() string { return a.kv.Reserved() }

func (a kitVerbAdapter) RunVerb(ctx context.Context, h *hostVerbResolver, op *spec.Op) spec.CheckResult {
	res := a.kv.RunVerb(ctx, hostCheckContext{h: h}, op)
	return spec.CheckResult{
		Op:      op,
		Verb:    a.kv.Reserved(),
		Status:  res.Status,
		Message: res.Message,
	}
}

// kitVerbActAdapter is the kitVerbAdapter variant for a host-coupled verb candy whose
// checkstep.CheckVerbProvider ALSO implements checkstep.ProvisionActor — a MULTI-ROLE state-provision
// verb (a check: probe AND a run:/build-act shell renderer). It adds the package-main
// ProvisionActor role, delegating RenderProvisionScript to the kit verb. A pure check verb
// stays a plain kitVerbAdapter, so it is NOT mis-resolved as a ProvisionActor by the act
// dispatch (resolveProvisionScript's type-assert); registerCompiledCheckVerb picks this
// variant only when the candy implements checkstep.ProvisionActor.
type kitVerbActAdapter struct {
	kitVerbAdapter
	pa checkstep.ProvisionActor
}

func (a kitVerbActAdapter) RenderProvisionScript(op *spec.Op, distros []string) (string, bool) {
	return a.pa.RenderProvisionScript(op, distros)
}

// Invoke serves the BUILD-context ops.OpEmit for a state-provision verb UNIFORMLY, so the
// render dispatches every plugin verb through Invoke(ops.OpEmit) with NO package-main
// concrete-type assert (the former prov.(ProvisionActor) branch in the EmitPluginOp
// render path). It decodes the full op from op.Params (a state-provision act reads
// SHARED #Op modifiers — mode/content — beyond plugin_input) + the BuildEnv distros
// from op.Env, renders the act shell via the kit ProvisionActor, and returns an
// EmitReply with ActScript=true so the render RUN-wraps it via EmitCmd (byte-identical
// to the former IsScript=true path) rather than splicing it verbatim. A non-ops.OpEmit op
// falls through to the embedded in-proc-only Invoke stub (a builtin verb still runs its
// check via RunVerb, never the wire envelope — the perf invariant is untouched). ok=false
// from RenderProvisionScript (the verb declined an act form) is the SAME hard error the
// former render seam raised.
func (a kitVerbActAdapter) Invoke(ctx context.Context, op *Operation) (*Result, error) {
	if op.Op != ops.OpEmit {
		return a.kitVerbAdapter.Invoke(ctx, op)
	}
	var sop spec.Op
	if len(op.Params) > 0 {
		if err := json.Unmarshal(op.Params, &sop); err != nil {
			return nil, fmt.Errorf("verb %q ops.OpEmit: decode op: %w", a.kv.Reserved(), err)
		}
	}
	var env spec.BuildEnv
	if len(op.Env) > 0 {
		if err := json.Unmarshal(op.Env, &env); err != nil {
			return nil, fmt.Errorf("verb %q ops.OpEmit: decode build env: %w", a.kv.Reserved(), err)
		}
	}
	script, ok := a.pa.RenderProvisionScript(&sop, env.Distros)
	if !ok {
		return nil, fmt.Errorf("run: plugin verb %q is not act-capable (ProvisionActor declined)", a.kv.Reserved())
	}
	out, err := marshalJSON(spec.EmitReply{Fragment: script, ActScript: true})
	if err != nil {
		return nil, err
	}
	return &Result{JSON: out}, nil
}

// kitVerbActStepAdapter is the variant for a host-coupled verb candy whose kit verb ALSO
// implements checkstep.StepProvider — a TYPED-STEP state-provision verb (service/package) whose
// build/deploy act lowers into a typed InstallStep, not a shell. It adds the package-main
// TypedStepProvider role (LowersTo + ConstructStep) and DELEGATES both halves to the candy:
// LowersTo returns the candy's StepKind() (the internal IR spec.StepKind), and ConstructStep
// calls the candy's MaterializeStep(op, the four ctx scalars) — so core holds NO per-kind
// switch (the kernel/plugin boundary law: a kind switch in core is an incomplete seam). The
// load-bearing Reverse() stays on the step the candy builds (package main owns the reversal
// timeline). Embeds kitVerbActAdapter (service/package are also ProvisionActors — the runtime
// act-shell half).
type kitVerbActStepAdapter struct {
	kitVerbActAdapter
	sp checkstep.StepProvider
}

func (a kitVerbActStepAdapter) LowersTo() spec.StepKind {
	return a.sp.StepKind()
}

func (a kitVerbActStepAdapter) ConstructStep(op *spec.Op, ctx stepConstructCtx) spec.InstallStep {
	return a.sp.MaterializeStep(op, ctx.RunAsUser, ctx.CandyName, ctx.PkgFormat, ctx.DistroTags)
}

// registerCompiledCheckVerb registers a COMPILED-IN host-coupled verb candy: it wraps
// the candy's checkstep.CheckVerbProvider in a kitVerbAdapter and registers it (with the
// candy's CUE schema) through the SAME RegisterBuiltinPluginUnit gate an
// typed builtin verb uses (schema gated at process start, origin "builtin", so the
// coexist switch treats it like any compiled-in plugin). Called from the generated
// plugins_generated.go for a kit-shape candy named in charly.yml compiled_plugins.
// Distinct from registerCompiledPlugin (the pb/dual-placement path) because a kit verb
// is in-proc-only. The candy passes its RAW schema embed.FS + InputDefs; charly
// concatenates here via the public sdk/schemaconcat over the conventional "schema"
// subdir — the SAME concat contract a builtin/external schema goes through (R3). A
// read/concat failure is a build-time invariant violation (panic, like
// loadBuiltinPluginUnits).
func registerCompiledCheckVerb(kv spec.CheckVerbProvider, meta pb.PluginMetaServer) {
	// Read the concatenated CUE schema + the input-def map from the candy's shared NewMeta
	// (the SAME Describe → BuildCapabilities the out-of-process placement serves), so a kit
	// candy provides ONE NewMeta for both placements — no exported SchemaFS/SchemaDir/InputDefs
	// trio (R3, mirrors registerCompiledPlugin's meta-driven in-proc lift).
	caps, err := meta.Describe(context.Background(), &pb.Empty{})
	if err != nil {
		panic("registerCompiledCheckVerb " + kv.Reserved() + ": describe: " + err.Error())
	}
	cueSource := caps.GetSchemaCue()
	inputDefs := map[string]string{}
	for _, c := range caps.GetProvided() {
		if c.GetInputDef() != "" {
			inputDefs[c.GetClass()+":"+c.GetWord()] = c.GetInputDef()
		}
	}
	base := kitVerbAdapter{kv: kv}
	// Thread the capability's declared scalar-sugar primary (mirrors
	// buildUnitInProc's lift on the pb path, R3) so register()'s primaryCarrier
	// hook registers it into the parse-time desugar.
	for _, c := range caps.GetProvided() {
		if ProviderClass(c.GetClass()) == ClassVerb && c.GetWord() == kv.Reserved() {
			base.primary = c.GetPrimary()
		}
	}
	var prov Provider = base
	// A multi-role state-provision verb's kit verb also implements checkstep.ProvisionActor —
	// register the act-aware variant so the act dispatch (resolveProvisionScript) resolves
	// its RenderProvisionScript. A pure check verb stays the plain adapter (no act role).
	// A TYPED-STEP verb (service/package) additionally implements checkstep.StepProvider — wrap
	// the act variant once more so hostBuildConstructStep resolves it as a TypedStepProvider.
	if pa, ok := kv.(checkstep.ProvisionActor); ok {
		act := kitVerbActAdapter{kitVerbAdapter: base, pa: pa}
		prov = act
		if sp, ok := kv.(checkstep.StepProvider); ok {
			prov = kitVerbActStepAdapter{kitVerbActAdapter: act, sp: sp}
		}
	}
	RegisterBuiltinPluginUnit(PluginUnit{
		Providers: []Provider{prov},
		Schema:    PluginSchema{CueSource: cueSource, InputDefs: inputDefs},
	})
}

// primaryInput implements primaryCarrier (the scalar-sugar primary) for the
// kit-verb adapter family — the value receiver makes every embedding wrapper
// (act / act-step) carry it too.
func (a kitVerbAdapter) primaryInput() string { return a.primary }
