package main

import (
	"context"
	"time"

	"github.com/opencharly/sdk/kit"
	pb "github.com/opencharly/sdk/proto"
)

// runnerCheckContext adapts the live *Runner to kit.CheckContext — the surface a
// HOST-COUPLED verb candy consumes. It is a wrapper (NOT methods on *Runner) because
// *Runner already has fields named Exec/Mode/HTTPClient/DialTimeout/Box/Instance/
// Distros; a method of the same name would collide. DeployExecutor satisfies
// kit.Executor structurally (identical RunCapture + Kind signatures), so Exec()
// returns r.Exec straight through.
type runnerCheckContext struct{ r *Runner }

func (c runnerCheckContext) Exec() kit.Executor         { return c.r.Exec }
func (c runnerCheckContext) DialTimeout() time.Duration { return c.r.DialTimeout }

// HTTPDo issues the request from the host (in-process) via the SHARED host HTTP-do path
// (doHTTPRequest — the SAME builder the out-of-process CheckContextService.HTTPDo uses, R3),
// derived from the engine's base client r.HTTPClient.
func (c runnerCheckContext) HTTPDo(ctx context.Context, req kit.HTTPRequest) (kit.HTTPResponse, error) {
	return doHTTPRequest(ctx, c.r.HTTPClient, req)
}
func (c runnerCheckContext) Box() string           { return c.r.Box }
func (c runnerCheckContext) Instance() string      { return c.r.Instance }
func (c runnerCheckContext) Distros() []string     { return c.r.Distros }
func (c runnerCheckContext) AddBackground(pid int) { c.r.Scenario.AddBackground(pid) }
func (c runnerCheckContext) Mode() kit.RunMode {
	if c.r.Mode == RunModeBox {
		return kit.ModeBox
	}
	return kit.ModeLive
}

// kitVerbAdapter wraps a COMPILED-IN host-coupled verb candy's kit.CheckVerbProvider
// as a package-main CheckVerbProvider, so runOne dispatches it through the SAME
// providerRegistry path as an typed builtin verb. It passes the live *Runner as a
// kit.CheckContext and converts the returned kit.Result back to a CheckResult
// (stamping Op + Verb). It embeds builtinVerbBase for Class()=ClassVerb + the
// in-proc-only Invoke stub — a kit verb is in-process only (RunVerb needs the live
// *Runner, which cannot cross a process boundary).
type kitVerbAdapter struct {
	builtinVerbBase
	kv kit.CheckVerbProvider
}

func (a kitVerbAdapter) Reserved() string { return a.kv.Reserved() }

func (a kitVerbAdapter) RunVerb(ctx context.Context, r *Runner, op *Op) CheckResult {
	res := a.kv.RunVerb(ctx, runnerCheckContext{r: r}, op)
	return CheckResult{
		Op:            op,
		Verb:          a.kv.Reserved(),
		Status:        kitStatusToCheck(res.Status),
		Message:       res.Message,
		CapturedValue: res.CapturedValue,
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

func (a kitVerbActAdapter) RenderProvisionScript(op *Op, distros []string) (string, bool) {
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

func (a kitVerbActStepAdapter) LowersTo() StepKind {
	return kitStepKindToCharly(a.sp.StepKind())
}

func (a kitVerbActStepAdapter) ConstructStep(op *Op, layer *Candy, img *ResolvedBox) InstallStep {
	return materializeStep(a.sp.ConstructStepDescriptor(op), op, layer, img)
}

// kitStepKindToCharly maps the kit's StepKindName to charly's internal StepKind enum.
func kitStepKindToCharly(k kit.StepKindName) StepKind {
	switch k {
	case kit.StepKindServicePackaged:
		return StepKindServicePackaged
	case kit.StepKindSystemPackages:
		return StepKindSystemPackages
	}
	panic("kitStepKindToCharly: unknown kit step kind " + string(k))
}

// materializeStep rebuilds the real package-main InstallStep from a candy's
// kit.StepDescriptor, computing the package-main-only inputs (the run-as-resolved scope,
// the candy name) that the candy cannot. The load-bearing Reverse() lives on the built
// step (package main), unchanged from the typed builtin verb's ConstructStep.
func materializeStep(desc kit.StepDescriptor, op *Op, layer *Candy, img *ResolvedBox) InstallStep {
	userDir, _ := resolveUserSpec(op.RunAs, img)
	switch {
	case desc.ServicePackaged != nil:
		return &ServicePackagedStep{
			Unit:        desc.ServicePackaged.Unit,
			TargetScope: opStepScope(userDir),
			Enable:      desc.ServicePackaged.Enable,
			CandyName:   layer.Name,
		}
	case desc.SystemPackages != nil:
		// Repos/Copr/Options come from the top-level package cascade
		// (compileSystemPackageSteps), NOT a per-op run: {package} step — match the
		// pre-extraction lowering (Format + PhaseInstall + the cross-distro-resolved name).
		return &SystemPackagesStep{
			Format:   img.Pkg,
			Phase:    PhaseInstall,
			Packages: []string{kit.ResolvePackageName(desc.SystemPackages.Package, desc.SystemPackages.PackageMap, img.Tags)},
		}
	default:
		panic("materializeStep: empty StepDescriptor for verb in candy " + layer.Name)
	}
}

func kitStatusToCheck(s kit.Status) CheckStatus {
	switch s {
	case kit.StatusFail:
		return TestFail
	case kit.StatusSkip:
		return TestSkip
	default:
		return TestPass
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
