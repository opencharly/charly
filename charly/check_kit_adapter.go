package main

import (
	"context"
	"time"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"

	"github.com/opencharly/sdk/kit"
	pb "github.com/opencharly/sdk/proto"
)

// hostCheckContext adapts the live check pass to kit.CheckContext — the surface a HOST-COUPLED
// verb candy consumes. It wraps the *hostVerbResolver (which holds the kit.Runner engine state +
// the per-Invoke endpoint cleanups) rather than the runner directly, so the six host-reverse legs
// (HTTPDo + the four Resolve* podman/go-libvirt/ssh ops) share ONE cleanup lifecycle with the
// out-of-process dispatch. The engine-state legs read the kit.Runner accessors; the host-reverse
// legs (check_endpoint_resolve.go) call the SAME resolveVerb* machinery the out-of-proc
// checkContextReverseServer uses — one source, two consumers, endpoint-identical (R3).
// DeployExecutor satisfies kit.Executor structurally, so Exec() returns the runner's straight
// through.
type hostCheckContext struct{ h *hostVerbResolver }

var _ kit.CheckContext = hostCheckContext{}

func (c hostCheckContext) Exec() kit.Executor         { return c.h.kr.Exec() }
func (c hostCheckContext) DialTimeout() time.Duration { return c.h.kr.DialTimeout() }

// HTTPDo issues the request from the host (in-process) via the SHARED host HTTP-do path
// (kit.DoHTTPRequest — the SAME builder the out-of-process CheckContextService.HTTPDo uses, R3),
// derived from the engine's base client.
func (c hostCheckContext) HTTPDo(ctx context.Context, req kit.HTTPRequest) (kit.HTTPResponse, error) {
	return kit.DoHTTPRequest(ctx, c.h.kr.HTTPClient(), req)
}
func (c hostCheckContext) Box() string           { return c.h.kr.Box() }
func (c hostCheckContext) Instance() string      { return c.h.kr.Instance() }
func (c hostCheckContext) Distros() []string     { return c.h.kr.Distros() }
func (c hostCheckContext) AddBackground(pid int) { c.h.kr.Scenario().AddBackground(pid) }
func (c hostCheckContext) Mode() kit.RunMode     { return c.h.kr.Mode() }

// kitVerbAdapter wraps a COMPILED-IN host-coupled verb candy's kit.CheckVerbProvider
// as a package-main CheckVerbProvider, so runOne dispatches it through the SAME
// providerRegistry path as an typed builtin verb. It passes the live check context
// (hostCheckContext over the *hostVerbResolver) as a kit.CheckContext and converts the
// returned kit.Result back to a CheckResult (stamping Op + Verb). It embeds
// builtinVerbBase for Class()=ClassVerb + the in-proc-only Invoke stub — a kit verb is
// in-process only (RunVerb needs the live host context, which cannot cross a process
// boundary).
type kitVerbAdapter struct {
	builtinVerbBase
	kv kit.CheckVerbProvider
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
// kit.CheckVerbProvider ALSO implements kit.ProvisionActor — a MULTI-ROLE state-provision
// verb (a check: probe AND a run:/build-act shell renderer). It adds the package-main
// ProvisionActor role, delegating RenderProvisionScript to the kit verb. A pure check verb
// stays a plain kitVerbAdapter, so it is NOT mis-resolved as a ProvisionActor by the act
// dispatch (resolveProvisionScript's type-assert); registerCompiledCheckVerb picks this
// variant only when the candy implements kit.ProvisionActor.
type kitVerbActAdapter struct {
	kitVerbAdapter
	pa kit.ProvisionActor
}

func (a kitVerbActAdapter) RenderProvisionScript(op *spec.Op, distros []string) (string, bool) {
	return a.pa.RenderProvisionScript(op, distros)
}

// kitVerbActStepAdapter is the variant for a host-coupled verb candy whose kit verb ALSO
// implements kit.StepProvider — a TYPED-STEP state-provision verb (service/package) whose
// build/deploy act lowers into a typed InstallStep, not a shell. It adds the package-main
// TypedStepProvider role (LowersTo + ConstructStep), materializing the candy's
// kit.StepDescriptor into the real ServicePackagedStep / SystemPackagesStep — so
// compileActOp lowers it exactly as the typed builtin verb did, and the load-bearing
// Reverse() stays in package main. Embeds kitVerbActAdapter (service/package are also
// ProvisionActors — the runtime act-shell half).
type kitVerbActStepAdapter struct {
	kitVerbActAdapter
	sp kit.StepProvider
}

func (a kitVerbActStepAdapter) LowersTo() spec.StepKind {
	return kitStepKindToCharly(a.sp.StepKind())
}

func (a kitVerbActStepAdapter) ConstructStep(op *spec.Op, ctx stepConstructCtx) spec.InstallStep {
	return materializeStep(a.sp.ConstructStepDescriptor(op), ctx)
}

// kitStepKindToCharly maps the kit's StepKindName to charly's internal StepKind enum.
func kitStepKindToCharly(k kit.StepKindName) spec.StepKind {
	switch k {
	case kit.StepKindServicePackaged:
		return spec.StepKindServicePackaged
	case kit.StepKindSystemPackages:
		return spec.StepKindSystemPackages
	}
	panic("kitStepKindToCharly: unknown kit step kind " + string(k))
}

// materializeStep rebuilds the real package-main InstallStep from a candy's
// kit.StepDescriptor and the pre-resolved stepConstructCtx (the run-as-resolved scope,
// the candy name, the image package format + distro tags — the 4 scalars this function
// actually reads, never a full layer/img handle). The load-bearing Reverse() lives on
// the built step (package main), unchanged from the typed builtin verb's ConstructStep.
func materializeStep(desc kit.StepDescriptor, ctx stepConstructCtx) spec.InstallStep {
	switch {
	case desc.ServicePackaged != nil:
		return &deploykit.ServicePackagedStep{
			Unit:        desc.ServicePackaged.Unit,
			TargetScope: deploykit.OpStepScope(ctx.RunAsUser),
			Enable:      desc.ServicePackaged.Enable,
			CandyName:   ctx.CandyName,
		}
	case desc.SystemPackages != nil:
		// Repos/Copr/Options come from the top-level package cascade
		// (compileSystemPackageSteps), NOT a per-op run: {package} step — match the
		// pre-extraction lowering (Format + PhaseInstall + the cross-distro-resolved name).
		return &deploykit.SystemPackagesStep{
			Format:   ctx.PkgFormat,
			Phase:    spec.PhaseInstall,
			Packages: []string{kit.ResolvePackageName(desc.SystemPackages.Package, desc.SystemPackages.PackageMap, ctx.DistroTags)},
		}
	default:
		panic("materializeStep: empty StepDescriptor for verb in candy " + ctx.CandyName)
	}
}

// registerCompiledCheckVerb registers a COMPILED-IN host-coupled verb candy: it wraps
// the candy's kit.CheckVerbProvider in a kitVerbAdapter and registers it (with the
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
func registerCompiledCheckVerb(kv kit.CheckVerbProvider, meta pb.PluginMetaServer) {
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
	// A multi-role state-provision verb's kit verb also implements kit.ProvisionActor —
	// register the act-aware variant so the act dispatch (resolveProvisionScript) resolves
	// its RenderProvisionScript. A pure check verb stays the plain adapter (no act role).
	// A TYPED-STEP verb (service/package) additionally implements kit.StepProvider — wrap
	// the act variant once more so compileActOp resolves it as a TypedStepProvider.
	if pa, ok := kv.(kit.ProvisionActor); ok {
		act := kitVerbActAdapter{kitVerbAdapter: base, pa: pa}
		prov = act
		if sp, ok := kv.(kit.StepProvider); ok {
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
