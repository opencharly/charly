package main

import (
	"context"
	"fmt"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/spec/spec"
)

// oci_step_emit.go — the host-side half of the pod-overlay step-emit dispatch. K5-A item 2
// relocated the FULL core provider-registry dispatch (~130 LOC of registry-consult + StepContract
// gating + Invoke logic) into candy/plugin-installstep's "oci-dispatch" word
// (candy/plugin-installstep/oci_dispatch.go) — the SAME plugin that already serves the other 12
// compiler-emitted kinds' build-emit, so the
// dispatch DECISION (which peer provider renders a given step) now lives alongside the words it
// routes to, reached via the class-generic DescribeProvider (cached metadata, K5-A item 1) +
// InvokeProvider (dispatch) reverse-channel legs instead of a direct in-core providerRegistry
// consult. What remains here is the THIN forwarding half every out-of-process caller
// (candy/plugin-deploy-pod, via deploykit.OCITarget.EmitStepOp → HostBuild("step-emit",
// "oci-emit-step")) still needs: resolve the compiled-in "oci-dispatch" provider, thread the SAME
// in-proc reverse channel + host-side buildEngineContext the former host-only dispatch used to
// thread (so a HOST-COUPLED peer's own OpEmit can still call back HostBuild — the
// "resolved-project" envelope seam, K5-Unit-6b), and Invoke.
//
// dispatchOCIStep is the single source of truth (R3): charly/step_emit_hostbuild.go's
// stepEmitOCIEmitStep (the production "oci-emit-step" render-seam handler) and ociEmitStep (below,
// a Go-object-typed compatibility wrapper existing unit tests drive directly) both funnel through it.

// dispatchOCIStep forwards ONE pod-overlay step's rendering to candy/plugin-installstep's
// "oci-dispatch" word: marshal the step/plan WIRE VIEWS + the caller's Distros/build-context
// scalars as the OpEmit payload/env, Invoke, and return the rendered fragment verbatim. `build`
// carries the host-side buildEngineContext whose scalars ride the class:step OpEmit's BuildEnv
// (Image/DevLocalPkg/ImageBuildDir/ContextRelPrefix) — see candy/plugin-installstep's package doc
// for which words are HOST-COUPLED vs PURE.
func dispatchOCIStep(stepView spec.InstallStepView, planView spec.InstallPlanView, distros []string, build buildEngineContext) (string, error) {
	prov, ok := providerRegistry.resolve(ClassStep, "oci-dispatch")
	if !ok {
		return "", fmt.Errorf("oci-emit-step: class:step provider %q not connected at build time", "oci-dispatch")
	}
	ctx := sdk.ContextWithExecutor(context.Background(),
		sdk.NewInProcExecutor(&inprocExecutorClient{srv: &executorReverseServer{build: build}}))
	env := spec.BuildEnv{Distros: distros, ImageBuildDir: build.ImageBuildDir, ContextRelPrefix: build.ContextRelPrefix}
	if build.Box != nil {
		env.Image = build.Box.Name
	}
	if build.Generator != nil {
		env.DevLocalPkg = build.Generator.DevLocalPkg
		// Thread the host's OWN original add_candy refs (hostBuildOverlay constructed this
		// Generator with loaderkit.ResolveOpts.ExtraCandyRefs = the deploy's add_candy: refs, possibly
		// REMOTE/qualified — e.g. "@github.com/…:vTAG") as ExtraCandyRefs, so
		// candy/plugin-installstep's getGenerator widens ITS OWN independent "resolved-project"
		// re-fetch the SAME way. This must be the ORIGINAL refs, NOT a re-derivation from
		// build.Generator.Candies's map KEYS: those keys are the SCAN RESULT's bare candy names
		// (ScanAllCandyWithConfigOpts's combined map is bare-keyed even for a remote candy), and
		// re-passing a bare name as an ExtraCandyRefs entry is a silent no-op (addRef gates on
		// IsRemoteCandyRef) — the bug this replaces: the bare-keys version never actually widened
		// the second fetch, so a remote add_candy candy was STILL absent from the plugin's own
		// envelope and candyByName's fallback still missed (RCA'd K1-alpha regression:
		// check-addcandy-pod's overlay-deploy path, "task emit: candy %q not found").
		env.ExtraCandyRefs = build.Generator.ExtraCandyRefs
	}
	params, err := marshalJSON(deploykit.OCIEmitStepParams{StepView: stepView, PlanView: planView})
	if err != nil {
		return "", fmt.Errorf("oci-emit-step: marshal dispatch params: %w", err)
	}
	frag, err := invokeOpEmitFragmentOpt(ctx, prov, "oci-dispatch", params, env, true)
	if err != nil {
		return "", fmt.Errorf("oci-dispatch: %w", err)
	}
	return frag, nil
}

// ociEmitStep is a Go-object-typed compatibility wrapper over dispatchOCIStep: it serializes step
// and plan to their wire views (the SAME serialization the production "oci-emit-step" caller
// already sends) and forwards through the identical seam. Kept for the existing unit-test suite
// (apk_format_test.go, build_target_oci_test.go, localpkg_test.go, plugin_externalstep_e2e_test.go),
// which drives ociEmitStep with concrete spec.InstallStep/spec.InstallPlan values rather than
// pre-marshaled wire views — a real, non-mocked path through the SAME relocated dispatch, not a
// parallel implementation (R3).
func ociEmitStep(step spec.InstallStep, plan *spec.InstallPlan, distros []string, build buildEngineContext) (string, error) {
	return dispatchOCIStep(spec.StepToView(step), spec.WireView(plan), distros, build)
}
