package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// oci_step_emit.go — the CORE pod-overlay step-emit dispatch, relocated out of
// build_target_oci.go's deploykit.OCITarget methods (P11c) into standalone funcs so the deploykit.OCITarget
// WALKER can live in sdk/deploykit (the walker delegates here through the "oci-emit-step"
// render-seam). This is the kind-blind host-side M-mechanism that STAYS core: it resolves
// each InstallStep kind through the providerRegistry + Invokes the class:step plugin OpEmit
// (spliceClassStepEmit) or the in-proc StepProvider.EmitOCI (ExternalPlugin), reusing the
// EXACT former in-core emitStep/spliceClassStepEmit logic — so the rendered fragment is
// byte-identical to the pre-move core render (byte-parity by construction, mirroring #67's
// render-seam contract). The live host-side buildEngineContext (the overlay *Generator +
// DistroDef/BuilderConfig/Box/ImageBuildDir/ContextRelPrefix, cached by hostBuildOverlay's
// prep+resolve) is passed in as `build`; a live *Generator cannot cross the wire, so the
// "oci-emit-step" render-seam looks it up from the per-dir overlayBuildContextCache. `build`'s
// scalars (Image/DevLocalPkg/ImageBuildDir/ContextRelPrefix) ride the class:step OpEmit's BuildEnv
// (ociSpliceClassStepEmit); the four former HOST-COUPLED step-emitters (system-packages/builder/
// local-pkg-install/op) render DIRECTLY in candy/plugin-installstep against its OWN
// "resolved-project"-built deploykit.Generator — there is no more in-core render for them.

// ociEmitStep renders ONE InstallStep's pod-overlay Containerfile fragment via the core
// provider-registry dispatch. It is the single source of truth (R3): the transitional in-core
// ociEmitStep delegates here, and (after the walker moves to sdk/deploykit) the candy's
// deploykit.OCITarget reaches it through the "oci-emit-step" render-seam. The returned fragment
// has its trailing newline normalized (matching the former per-arm t.buf behaviour); an empty
// return is a deploy-only / VenueSkip step (records nothing). `build` carries the host-side
// buildEngineContext whose scalars ride onto the class:step OpEmit's BuildEnv (Image/DevLocalPkg/
// ImageBuildDir/ContextRelPrefix) for the HOST-COUPLED words (system-packages/builder/local-pkg/op),
// which render directly in the plugin — no in-core renderer is consulted for them.
func ociEmitStep(step spec.InstallStep, plan *deploykit.InstallPlan, distros []string, build buildEngineContext) (string, error) {
	var (
		frag string
		err  error
	)
	switch {
	case deploykit.IsExternalStepKind(step.Kind()):
		// F-STEP-EMIT: an authored external step ("external:<word>") — its serving provider is a
		// class:step plugin keyed on the trimmed word; OpEmit bakes its build-context fragment
		// (Emits=true) or is a no-op (Emits=false, deploy-only). allowEmpty=false: an authored
		// external step MUST produce a fragment.
		s := step.(*deploykit.ExternalStep)
		frag, err = ociSpliceClassStepEmit(s.Word, s.Payload, distros, false, build)
	case pluginEmitStepWords[step.Kind()] != "":
		// C1.1–C1.6: the 12 compiler-emitted kinds whose build-emit externalized to the
		// compiled-in class:step plugin candy/plugin-installstep. Route by kind→word, passing
		// the compiler's step VIEW (stepToView) as the opaque OpEmit payload (the SAME
		// serialization the deploy walk consumes, R3). allowEmpty=true: a legitimately-empty
		// render (empty snippet / no-op service) is tolerated; a no-op-emit kind (apk/reboot,
		// Emits=false) is skipped inside ociSpliceClassStepEmit.
		word := pluginEmitStepWords[step.Kind()]
		payload, merr := marshalJSON(deploykit.StepToView(step))
		if merr != nil {
			return "", fmt.Errorf("oci-emit-step: marshal %s step view: %w", step.Kind(), merr)
		}
		frag, err = ociSpliceClassStepEmit(word, payload, distros, true, build)
	default:
		// The ONE remaining in-proc StepProvider kind (ExternalPlugin — a plugin-verb run:
		// step). EmitOCI returns the fragment (P11c decoupled it from the the walker buffer).
		prov, ok := stepProviderFor(step.Kind())
		if !ok {
			return "", fmt.Errorf("oci-emit-step: unknown step kind %q", step.Kind())
		}
		frag, err = prov.EmitOCI(step, plan, build)
	}
	if err != nil {
		return "", err
	}
	if frag == "" {
		return "", nil
	}
	if !strings.HasSuffix(frag, "\n") {
		frag += "\n"
	}
	return frag, nil
}

// ociSpliceClassStepEmit resolves the class:step provider serving `word`, consults its DECLARED
// StepContract.Emits, and — when the step emits — Invokes OpEmit with the opaque payload and
// returns the rendered Containerfile fragment verbatim (R3). Shared by the AUTHORED external
// step (allowEmpty=false) and the 12 COMPILER-EMITTED typed step kinds whose build-emit
// externalized to candy/plugin-installstep (allowEmpty=true). A provider declaring Emits=false is
// a DEPLOY-ONLY step (no build fragment) → returns "".
//
// The Invoke ctx carries an IN-PROC reverse channel (sdk.ContextWithExecutor + executorReverseServer,
// the SAME one dispatchBuild threads for the compiled-in build:box plugin, R3), threaded with the
// host-side buildEngineContext (`build`), so a HOST-COUPLED step can call back HostBuild
// ("resolved-project") for the project structure it needs. The per-invocation scalars `build` already
// carries (Image/DevLocalPkg/ImageBuildDir/ContextRelPrefix) ride the SAME OpEmit Invoke's BuildEnv —
// no separate round-trip: a HOST-COUPLED step's OpEmit builds its OWN deploykit.Generator from the
// resolved-project envelope and renders directly (candy/plugin-installstep), rather than calling back
// a host-side renderer.
func ociSpliceClassStepEmit(word string, payload []byte, distros []string, allowEmpty bool, build buildEngineContext) (string, error) {
	prov, ok := providerRegistry.resolve(ClassStep, word)
	if !ok {
		return "", fmt.Errorf("oci-emit-step: class:step provider %q not connected at build time", word)
	}
	emits := false
	if carrier, ok := prov.(spec.StepContractCarrier); ok {
		if sc, ok := carrier.DeclaredStepContract(); ok {
			emits = sc.Emits
		}
	}
	if !emits {
		// A deploy-only step (like apk on an image build): recorded, not baked.
		return "", nil
	}
	ctx := sdk.ContextWithExecutor(context.Background(),
		sdk.NewInProcExecutor(&inprocExecutorClient{srv: &executorReverseServer{build: build}}))
	env := spec.BuildEnv{Distros: distros, ImageBuildDir: build.ImageBuildDir, ContextRelPrefix: build.ContextRelPrefix}
	if build.Box != nil {
		env.Image = build.Box.Name
	}
	if build.Generator != nil {
		env.DevLocalPkg = build.Generator.DevLocalPkg
	}
	frag, err := invokeOpEmitFragmentOpt(ctx, prov, word, payload, env, allowEmpty)
	if err != nil {
		return "", fmt.Errorf("class:step %q build-emit: %w", word, err)
	}
	return frag, nil
}
