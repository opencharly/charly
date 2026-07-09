package main

// build_target_oci.go — OCITarget implements DeployTarget for Containerfile
// emission: the POD-OVERLAY target that synthesizes an add_candy overlay
// Containerfile at DEPLOY (charly bundle add of a pod carrying add_candy:).
// `charly box build`/`generate` do NOT use OCITarget — they emit directly via
// generate.go writeCandySteps→emitTasks; the IR/OCITarget path is deploy-only.
//
// OCITarget is a thin walker over the InstallPlan that delegates to the
// format/template rendering machinery in sdk/buildkit + tasks.go. Every
// compiler-emitted step kind's BUILD-emit is served by the compiled-in class:step
// plugin candy/plugin-installstep (routed by pluginEmitStepWords through
// spliceClassStepEmit); the HOST-COUPLED kinds (system-packages/builder/
// local-pkg-install/op) call back the host build engine over the in-proc reverse
// channel (step_emit_hostbuild.go), so the Op build-emit still flows through the
// SAME Generator.emitTasks seam the box build uses and the overlay stays
// functionally equivalent for one candy.
//
// The key property we want from OCITarget: feeding it a plan produced
// by BuildDeployPlan must emit a Containerfile fragment that's
// functionally equivalent to what today's writeCandySteps produces for
// the same candy. Not byte-identical (we've dropped that requirement
// per the user) but semantically equivalent — same packages installed,
// same tasks executed, same services configured.

import (
	"context"
	"fmt"
	"strings"

	"github.com/opencharly/sdk"
)

// OCITarget emits Containerfile directives for an InstallPlan. One
// instance handles one image build; callers create a new target per
// image and call Emit with the plan set for that image.
type OCITarget struct {
	// DistroDef is the resolved per-image distro definition — needed so
	// OCITarget can look up format install_templates and cache mounts.
	DistroDef *DistroDef

	// BuilderConfig is the builder registry for this image — used to
	// render multi-stage builders when the IR contains BuilderStep.
	BuilderConfig *BuilderConfig

	// Box, BuildDir, ContextRelPrefix mirror the state the legacy
	// Generator carries for emit-time rendering. Populated by callers
	// before Emit when they want full task + builder rendering (not
	// just the placeholder output). Safe to leave zero for tests.
	Box              *ResolvedBox
	BuildDir         string
	ContextRelPrefix string
	Generator        *Generator // used for emitTasks + builder stage rendering

	// Buffer collects the rendered Containerfile fragment. Callers
	// read it via String() after Emit completes.
	buf strings.Builder
}

// Name identifies this target.
func (t *OCITarget) Name() string { return "oci" }

// Emit walks each plan's steps and appends Containerfile directives to
// the internal buffer. Multiple plans emit sequentially (per-candy).
func (t *OCITarget) Emit(plans []*InstallPlan, opts EmitOpts) error {
	for _, plan := range plans {
		if plan == nil {
			continue
		}
		if err := t.emitPlan(plan, opts); err != nil {
			return fmt.Errorf("OCITarget.Emit(%s): %w", plan.Candy, err)
		}
	}
	return nil
}

// String returns the accumulated Containerfile fragment.
func (t *OCITarget) String() string {
	return t.buf.String()
}

// emitPlan emits directives for one candy's plan.
func (t *OCITarget) emitPlan(plan *InstallPlan, _ EmitOpts) error {
	// Resolve the deferred {{.Home}} token in home-bearing step fields to
	// the image's runtime home. For an OCI build (and the pod-overlay build
	// that reuses OCITarget) img.Home IS the home the baked paths run under.
	if t.Box != nil {
		planResolveHome(plan, t.Box.Home)
	}
	fmt.Fprintf(&t.buf, "# Layer: %s\n", plan.Candy)
	for _, step := range plan.Steps {
		if step.Venue() == VenueSkip {
			continue
		}
		// Gates don't apply to OCI emission — container builds are
		// already isolated, so the opt-in flags mean nothing here.
		if err := t.emitStep(step, plan); err != nil {
			return err
		}
	}
	t.buf.WriteString("\n")
	return nil
}

// emitStep dispatches each step to its StepProvider's OCI emitter (the per-kind
// type-switch is gone — C4). The skip-on-image-build behaviour for apk/reboot,
// and the localpkg PRODUCTION-vs-checkbed install decision, live on their
// providers' EmitOCI (step_builtins.go).
func (t *OCITarget) emitStep(step InstallStep, plan *InstallPlan) error {
	// F-STEP-EMIT: an EXTERNAL (plugin-contributed) step kind ("external:<word>") has no
	// compiled-in StepProvider registered under its full kind string — its serving provider
	// is a class:step plugin keyed on the trimmed WORD. The open arm resolves it and bakes its
	// build-context fragment (Emits=true) via OpEmit, or skips it (Emits=false, a deploy-only
	// step). This is the BUILD leg that lets C1 externalize a step kind whose EmitOCI produces
	// a Containerfile fragment; the DEPLOY leg (executeExternalStep) already exists (F3).
	if isExternalStepKind(step.Kind()) {
		return t.emitExternalStep(step.(*externalStep), plan)
	}
	// C1.1: the seven PURE builtin step kinds' BUILD-emit is served by the compiled-in class:step
	// plugin candy/plugin-installstep (its OpEmit). Route them by kind→word, passing the compiler's
	// step VIEW as the opaque payload — the SAME serialization the deploy walk consumes (R3). Their
	// DEPLOY leg is unchanged: sdk/kit.WalkPlans renders them from that same view. A
	// legitimately-empty render (empty snippet / no-op service) is allowed (allowEmpty), matching
	// what the former OCITarget.emit* returned for those instances.
	if word, ok := pluginEmitStepWords[step.Kind()]; ok {
		payload, err := marshalJSON(stepToView(step))
		if err != nil {
			return fmt.Errorf("OCITarget: marshal %s step view: %w", step.Kind(), err)
		}
		return t.spliceClassStepEmit(word, payload, true)
	}
	prov, ok := stepProviderFor(step.Kind())
	if !ok {
		return fmt.Errorf("OCITarget: unknown step kind %q", step.Kind())
	}
	return prov.EmitOCI(t, step, plan)
}

// emitExternalStep bakes an EXTERNAL (plugin-contributed) step kind's build-context Containerfile
// fragment (F-STEP-EMIT). An authored external step declaring Emits=true must produce a fragment
// (allowEmpty=false), unlike the compiler-emitted typed steps above.
func (t *OCITarget) emitExternalStep(s *externalStep, _ *InstallPlan) error {
	return t.spliceClassStepEmit(s.Word, s.Payload, false)
}

// spliceClassStepEmit resolves the class:step provider serving `word`, consults its DECLARED
// StepContract.Emits, and — when the step emits — Invokes OpEmit with the opaque payload and
// splices the returned Containerfile fragment verbatim (R3). Shared by the AUTHORED external step
// (emitExternalStep, F3) and the seven COMPILER-EMITTED typed step kinds whose build-emit
// externalized to candy/plugin-installstep (emitStep's pluginEmitStepWords route, C1.1). A provider
// declaring Emits=false is a DEPLOY-ONLY step (no build fragment) and is a no-op on the image build,
// exactly like ApkInstall/Reboot skip. allowEmpty permits a legitimately-empty fragment from a
// compiler-emitted step (an authored external step must not return empty).
//
// The Invoke ctx carries an IN-PROC reverse channel (the SAME sdk.ContextWithExecutor +
// executorReverseServer that dispatchBuild threads for the compiled-in build:box plugin, R3),
// threaded with the box BUILD-ENGINE context (stepEmitBuildContext — the box-resolved DistroDef),
// so a HOST-COUPLED step can call back HostBuild("step-emit", …) during its OpEmit — the host build
// ENGINE stays in core (the step-emit seam), the plugin only REQUESTS it. The compiler-emitted
// system-packages kind (C1.2) takes this path (its build-emit needs DistroDef.Format); a PURE step
// (file/shell-hook/…) ignores the channel and returns its fragment directly.
func (t *OCITarget) spliceClassStepEmit(word string, payload []byte, allowEmpty bool) error {
	prov, ok := providerRegistry.resolve(ClassStep, word)
	if !ok {
		return fmt.Errorf("OCITarget: class:step provider %q not connected at build time", word)
	}
	emits := false
	if carrier, ok := prov.(stepContractCarrier); ok {
		if sc, ok := carrier.declaredStepContract(); ok {
			emits = sc.Emits
		}
	}
	if !emits {
		// A deploy-only step (like apk on an image build): recorded, not baked.
		return nil
	}
	var distros []string
	if t.Box != nil {
		distros = t.Box.Tags
	}
	ctx := sdk.ContextWithExecutor(context.Background(),
		sdk.NewInProcExecutor(&inprocExecutorClient{srv: &executorReverseServer{build: t.stepEmitBuildContext()}}))
	frag, err := invokeOpEmitFragmentOpt(ctx, prov, word, payload, distros, allowEmpty)
	if err != nil {
		return fmt.Errorf("class:step %q build-emit: %w", word, err)
	}
	if frag == "" {
		return nil
	}
	t.buf.WriteString(frag)
	if !strings.HasSuffix(frag, "\n") {
		t.buf.WriteString("\n")
	}
	return nil
}

// stepEmitBuildContext is the host BUILD-ENGINE context threaded onto the in-proc reverse channel
// spliceClassStepEmit stands up, so a HOST-COUPLED class:step plugin can call back
// HostBuild("step-emit", …) during its OpEmit and reach the host build engine. It carries the
// box-resolved DistroDef wrapped as a DistroConfig (wrapDistroDef) — the datum the SystemPackages
// step-emitter needs to resolve the format's phase.install.container template (the C1.2 relocation
// of the SystemPackages build-emit onto the step-emit seam) — plus the Generator + BuilderConfig +
// Box the Builder step-emitter needs to render a multi-stage / inline builder via the SAME
// buildStageContext + RenderTemplate pipeline (the C1.3 relocation of the Builder build-emit onto
// the same seam), plus the Generator (DevLocalPkg) + Box (Name) + ImageBuildDir the LocalPkgInstall
// step-emitter needs to render the dev/prod localpkg IMAGE install via renderLocalPkgImageInstall
// (the C1.4 relocation), plus the Generator + Box + ImageBuildDir + ContextRelPrefix the Op
// step-emitter needs to drive the SAME Generator.emitTasks per-verb render pipeline the box build
// uses (the C1.5 relocation of the OpStep build-emit onto the same seam). A PURE step
// (file/shell-hook/…) ignores the channel entirely.
func (t *OCITarget) stepEmitBuildContext() buildEngineContext {
	return buildEngineContext{
		DistroCfg:        wrapDistroDef(t.DistroDef),
		Generator:        t.Generator,
		BuilderConfig:    t.BuilderConfig,
		Box:              t.Box,
		ImageBuildDir:    t.BuildDir,
		ContextRelPrefix: t.ContextRelPrefix,
	}
}

// formatDefCacheMountDefs returns the cache mounts as the type
// RenderTemplate's InstallContext expects. FormatDef.CacheMount is the
// source of truth; this is a no-op bridge.
func formatDefCacheMountDefs(f *FormatDef) []CacheMountDef {
	if f == nil {
		return nil
	}
	return f.CacheMount
}
