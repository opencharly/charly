package main

import (
	"context"
	"fmt"

	"github.com/opencharly/sdk/spec"
)

// CheckVerbProvider is the typed in-process form of a check-verb Provider: it
// runs the assert-mode probe and returns a CheckResult directly. It is threaded the
// host verb resolver (*hostVerbResolver — which holds the kit.Runner engine state and
// builds the live host CheckContext), NOT a serializable value, so it cannot cross the
// wire. Every built-in verb implements it; the verb dispatch resolves the verb through
// providerRegistry and calls RunVerb — the switch is gone.
//
// An OUT-OF-PROCESS plugin verb does NOT implement this — it is reached via the
// generic `plugin:` envelope (runPluginVerb → ResolveVerb → the Invoke wire
// form), so the dispatch only ever deals with in-proc CheckVerbProviders here.
type CheckVerbProvider interface {
	Provider
	RunVerb(ctx context.Context, h *hostVerbResolver, op *spec.Op) spec.CheckResult
}

// The EXTERNAL-CHARLY-VERBS kube/adb/appium/spice/mcp/record/cdp/vnc/dbus/wl/libvirt are
// live-container verbs served OUT-OF-PROCESS (candy/plugin-*); they are reached via the
// generic `plugin:` Invoke envelope (invokeVerbProvider), NOT a typed in-proc contract —
// their method allowlist + required-modifier checks live in the plugin, and their
// method-name enum is enforced by CUE on core #Op. The former in-proc live-verb seam
// (a compiled-in live verb owning its method contract + the host's subprocess dispatcher)
// was deleted once the externalization orphaned it.

// ProvisionActor is the optional do:act half of a verb provider: it renders the
// shell that performs a state-provision verb's side-effect on the live target
// (ok=false for a verb with no act form — an action verb whose handler already
// acts, or a pure observe verb). Only the state-provision verbs (file/package/
// user/group/kernel-param/mount, plus the runtime live-act path of `service`)
// implement it; runProvisionAct resolves the verb and type-asserts ProvisionActor —
// the per-verb act switch is gone (C1b).
type ProvisionActor interface {
	RenderProvisionScript(op *spec.Op, distros []string) (string, bool)
}

// stepConstructCtx is the NARROW, hand-written (non-wire — it never crosses a process
// boundary, so no CUE-sourcing debt) value TypedStepProvider.ConstructStep needs from
// the compile-time layer/image context — the S5 fix for the R-item typed-shape leak a
// core-defined interface previously carried (deploykit.CandyModel / *buildkit.ResolvedBox,
// both explicitly-named R-item shapes per the plugin skill's boundary-law section). Built
// once by compileActOp (which already resolves every field for its OWN OpStep/
// ExternalPluginStep construction) and threaded through unchanged.
type stepConstructCtx struct {
	// RunAsUser is the ALREADY-RESOLVED run-as directive (deploykit.ResolveUserSpec's
	// first return, computed by the caller from op.RunAs + the image's User/UID/GID/Home —
	// ConstructStep needs the resolved directive, never the raw image identity).
	RunAsUser string
	// CandyName is the authoring candy's name (layer.GetName()).
	CandyName string
	// PkgFormat is the image's primary build format (img.Pkg) — SystemPackagesStep's Format.
	PkgFormat string
	// DistroTags is the image's resolved distro/build-format tag union (img.Tags) —
	// SystemPackagesStep's package-name cascade (kit.ResolvePackageName).
	DistroTags []string
}

// TypedStepProvider is the do:act half of a verb provider whose build/deploy install
// timeline lowers into a TYPED InstallStep — NOT a RenderProvisionScript shell string.
// The ONE current member is `service`: its act constructs a ServicePackagedStep whose
// Reverse() records the LOAD-BEARING reversals (ReverseOpServiceDisable / RestoreEnabled
// / RemoveDropin) a shell string would drop. compileActOp resolves a `plugin:` verb's
// provider and, when it implements this, returns ConstructStep (the typed step flows
// through the SAME ServicePackagedStep.Emit{OCI,Local,VM} + Reverse() as before) instead
// of falling through to a generic OpStep. LowersTo names the step kind (the now-removed
// VerbSpec.LowersTo field's role — package/service were its only users, so the field was
// deleted and the lowering target lives on the provider); ConstructStep builds the step
// from the op's plugin_input plus the narrow stepConstructCtx (S5). A TypedStepProvider
// therefore also "acts in build/deploy" (opActsInBuildDeploy) even though it is not a
// ProvisionActor.
type TypedStepProvider interface {
	Provider
	LowersTo() spec.StepKind
	ConstructStep(op *spec.Op, ctx stepConstructCtx) spec.InstallStep
}

// BuildEmitter is the build-context act half of a verb provider that renders its
// install timeline as a verbatim Containerfile FRAGMENT via Invoke(OpEmit) — neither
// a RenderProvisionScript shell string (ProvisionActor) nor a typed InstallStep
// (TypedStepProvider). emitTasks resolves a `plugin:` verb's provider and, when it is
// NOT a ProvisionActor, renders the fragment via emitPluginFragment → Invoke(OpEmit)
// (placement-agnostic: in-proc for a builtin implementing this interface, over go-plugin
// gRPC for an external grpcProvider). A builtin verb implementing BuildEmitter therefore
// also "acts in build/deploy" (opActsInBuildDeploy) even though it is not a ProvisionActor.
// The EXTERNAL placement of the same plugin is a grpcProvider — the host cannot type-assert
// across the process boundary, so opActsInBuildDeploy recognizes that placement separately.
type BuildEmitter interface {
	Provider
	// BuildEmits marks the capability — it always returns true; the fragment itself is
	// produced by Invoke(OpEmit), not here. (A marker method, like LowersTo names a kind.)
	BuildEmits() bool
}

// builtinVerbBase supplies the in-proc-only Provider half (Class + a stub Invoke)
// for every built-in verb provider. A compiled-in verb runs via RunVerb; it does
// not serve itself out-of-process, so Invoke is an explicit error rather than a
// silent path. Each verb embeds this and adds Reserved() + RunVerb().
type builtinVerbBase struct{}

func (builtinVerbBase) Class() ProviderClass { return ClassVerb }
func (builtinVerbBase) Invoke(context.Context, *Operation) (*Result, error) {
	return nil, fmt.Errorf("built-in verb is in-process only (no out-of-proc Invoke)")
}

// checkVerbProviderBijection asserts every CUE-declared verb (spec.OpVerbs) has a
// registered in-proc CheckVerbProvider — the registry generalization of the
// VerbCatalog⇄spec.OpVerbs gate. Extra ClassVerb providers (out-of-tree plugin
// verbs, not in spec.OpVerbs) are ALLOWED — a plugin contributes new verbs. Runs
// at init() alongside the other bijection gates.
func checkVerbProviderBijection(verbs []string) error {
	var missing []string
	for _, v := range verbs {
		// Pure-install verbs (mkdir/copy/write/link/download/setcap/build) render
		// ONLY to install steps — runOne never check-dispatches them (it skips an
		// install verb authored as a check), so they need no CheckVerbProvider.
		// (`command` was the lone check-dispatched installVerb; it left spec.OpVerbs in
		// the command→plugin extraction, so this loop no longer sees it.)
		if spec.InstallVerbs[v] {
			continue
		}
		p, ok := providerRegistry.resolve(ClassVerb, v)
		if !ok {
			missing = append(missing, v)
			continue
		}
		if _, ok := p.(CheckVerbProvider); !ok {
			missing = append(missing, v+" (registered but not a CheckVerbProvider)")
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("reserved-word registry: check-dispatch verbs in spec.OpVerbs with no in-proc CheckVerbProvider: %v", missing)
	}
	return nil
}
