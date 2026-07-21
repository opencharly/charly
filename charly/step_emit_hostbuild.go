package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// step_emit_hostbuild.go — the F-STEP-EMIT "step-emit" host-builder on the F10 HostBuild seam.
// It USED to also be the BUILD-context counterpart of "overlay"/"image"/"containerfiles" for
// FOUR host-coupled external step kinds (system-packages/builder/local-pkg-install/op, C1.2-C1.5):
// their serving class:step plugin (candy/plugin-installstep) called back
// Executor.HostBuild("step-emit", StepEmitRequest{word,payload,distros}) during OpEmit, and this
// seam dispatched by WORD to an in-core renderer that needed the host build ENGINE (DistroDef
// format templates, the Generator's task/builder rendering) — machinery that could not cross the
// process boundary at the time. That is GONE (K5-Unit-6b): the plugin now fetches the
// "resolved-project" envelope ONCE per project dir (the SAME generic seam candy/plugin-box /
// candy/plugin-bundle / candy/plugin-check already consume), builds its OWN *deploykit.Generator
// from it via the shared deploykit.NewRenderGeneratorFromProject helper (the SAME construction
// source candy/plugin-build + candy/plugin-deploy-pod use), and renders all four words DIRECTLY —
// no per-render host round-trip, no in-core renderer for them at all. The per-invocation scalars
// those four words need (which box, dev-bed vs production, the inline-content staging dir) ride
// the SAME OpEmit Invoke's spec.BuildEnv (op.Env) — see charly/oci_step_emit.go's
// ociSpliceClassStepEmit.
//
// What REMAINS on this seam is "oci-emit-step" (stepEmitOCIEmitStep, below) — the pod-overlay
// candy's (candy/plugin-deploy-pod) OUT-OF-PROCESS per-step render request, which needs the FULL
// core provider-registry dispatch (ociEmitStep: resolves EVERY InstallStep kind — the 12
// compiler-emitted plugin-served kinds, the authored external step, and the in-proc ExternalPlugin
// StepProvider — not just the four former host-coupled ones) and therefore genuinely cannot be
// answered by the candy itself. The seam stays GENERIC (dispatches by word against the
// stepEmitters registry, no per-word case here), even though only one word remains registered
// today — a future out-of-process caller needing an in-core-only render reaches the SAME seam.

// stepEmitter renders one external step kind's build-context Containerfile fragment IN-CORE from
// the opaque request + the host build-engine context. Registered per step word.
type stepEmitter func(req spec.StepEmitRequest, build buildEngineContext) (string, error)

// stepEmitters maps a step WORD → its in-core fragment renderer. Populated at package-var init
// (before any init(), like hostBuilders / the substrate registries), so lookup is race-free.
var stepEmitters = map[string]stepEmitter{}

// registerStepEmitter records one step kind's in-core fragment renderer. Panics on
// a duplicate (a startup invariant, like registerHostBuilder).
func registerStepEmitter(word string, fn stepEmitter) {
	if word == "" || fn == nil {
		panic("registerStepEmitter: empty word or nil emitter")
	}
	if _, dup := stepEmitters[word]; dup {
		panic(fmt.Sprintf("registerStepEmitter: duplicate step emitter for %q", word))
	}
	stepEmitters[word] = fn
}

// stepEmitterFor returns the registered in-core emitter for a step word, if any.
func stepEmitterFor(word string) (stepEmitter, bool) {
	fn, ok := stepEmitters[word]
	return fn, ok
}

// hostBuildStepEmit is the "step-emit" host-builder (F10 HostBuild seam): decode the
// StepEmitRequest, dispatch by Word to the registered in-core emitter, and return the rendered
// fragment as an EmitReply JSON. An unregistered word is a LOUD error (a step word whose in-core
// renderer was never registered — never a silent empty bake, R4). The buildEngineContext
// carries the host engine the emitter renders against.
func hostBuildStepEmit(_ context.Context, req spec.StepEmitRequest, build buildEngineContext) (spec.EmitReply, error) {
	if req.Word == "" {
		return spec.EmitReply{}, fmt.Errorf("step-emit host-build: request carries no step word")
	}
	fn, ok := stepEmitterFor(req.Word)
	if !ok {
		return spec.EmitReply{}, fmt.Errorf("step-emit host-build: no in-core emitter registered for step %q", req.Word)
	}
	frag, err := fn(req, build)
	if err != nil {
		return spec.EmitReply{}, fmt.Errorf("step-emit host-build %q: %w", req.Word, err)
	}
	return spec.EmitReply{Fragment: frag}, nil
}

// Register the step-emit host-builder at package-var init (before any init(), like the
// image/overlay/plugin-binary builders).
var _ = func() bool {
	registerHostBuilder("step-emit", typedHostBuilder("step-emit", hostBuildStepEmit))
	return true
}()

// stepEmitOCIEmitStep renders ONE pod-overlay InstallStep's Containerfile fragment via the FULL
// core provider-registry dispatch (ociEmitStep) — the P11c overlay-BUILD dissolution. The candy
// (candy/plugin-deploy-pod podPrepareVenue) constructs a deploykit.OCITarget whose EmitStepOp
// seam calls HostBuild("step-emit", {Word:"oci-emit-step", Payload: deploykit.OCIEmitStepParams{
// Dir, StepView, PlanView}, Distros}) for each step; this emitter reconstructs the step + the plan
// from their wire views (stepFromView/PlanFromView) + calls ociEmitStep (the SAME single source of
// truth the in-core ociEmitStep delegates to: the 12 compiler-emitted plugin-served kinds +
// the authored external step via spliceClassStepEmit, ExternalPlugin via stepProviderFor.EmitOCI),
// returning the rendered fragment byte-identical to the former in-core overlay render.
//
// The buildEngineContext: an IN-PROC caller cannot occur for this word (only the OUT-OF-PROCESS
// pod-overlay candy calls "oci-emit-step" — a compiled-in class:step plugin's OpEmit never asks
// for its OWN fragment through this seam), so `build` always arrives empty here and the emitter
// looks up the cached overlay buildEngineContext by Dir (loadOverlayBuildContext, populated by
// hostBuildOverlay's prep). The FOUR former host-coupled per-word sub-dispatches
// (system-packages/builder/local-pkg/op) that ociEmitStep's ociSpliceClassStepEmit used to route
// back onto THIS seam no longer do — candy/plugin-installstep renders them directly against its
// own "resolved-project"-built Generator now (K5-Unit-6b), so ociEmitStep's dispatch into
// class:step plugins for those words is a single Invoke with no re-entrant HostBuild("step-emit").
func stepEmitOCIEmitStep(req spec.StepEmitRequest, build buildEngineContext) (string, error) {
	var p deploykit.OCIEmitStepParams
	if len(req.Payload) > 0 {
		if err := json.Unmarshal(req.Payload, &p); err != nil {
			return "", fmt.Errorf("decode oci-emit-step params: %w", err)
		}
	}
	step, err := deploykit.StepFromView(p.StepView)
	if err != nil {
		return "", fmt.Errorf("oci-emit-step: reconstruct step: %w", err)
	}
	plan, err := deploykit.PlanFromView(p.PlanView)
	if err != nil {
		return "", fmt.Errorf("oci-emit-step: reconstruct plan: %w", err)
	}
	// Out-of-process overlay caller: build is empty → look up the cached overlay buildEngineContext.
	if build.Generator == nil && p.Dir != "" {
		if cached := loadOverlayBuildContext(p.Dir); cached != nil {
			build = *cached
		}
	}
	return ociEmitStep(step, plan, req.Distros, build)
}

// Register the oci-emit-step emitter at package-var init — the overlay pod-overlay build's
// per-step render seam (P11c). The candy (candy/plugin-deploy-pod) wires deploykit.OCITarget's
// EmitStepOp to HostBuild("step-emit", {Word:"oci-emit-step", …}).
var _ = func() bool { registerStepEmitter("oci-emit-step", stepEmitOCIEmitStep); return true }()
