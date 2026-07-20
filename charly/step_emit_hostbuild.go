package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// step_emit_hostbuild.go — the F-STEP-EMIT "step-emit" host-builder on the F10 HostBuild seam:
// the BUILD-context counterpart of "overlay"/"image"/"containerfiles". A HOST-COUPLED external
// step kind — one whose build-context Containerfile fragment needs the host build ENGINE (the
// DistroDef format templates, the Generator's task/builder rendering) that cannot cross the
// process boundary — has its serving class:step plugin call back Executor.HostBuild("step-emit",
// StepEmitRequest{word,payload,distros}) during its OpEmit. This host-builder dispatches by the
// step WORD to a registered per-word emitter that renders the fragment IN-CORE and returns it as
// an EmitReply (reusing EmitReply — R3). A PURE external step never reaches here: it returns its
// fragment directly from OpEmit (ociEmitStep splices that).
//
// The per-word emitter registry (stepEmitters) holds one renderer per relocated host-coupled step
// kind. C1.2 registered the FIRST — system-packages (stepEmitSystemPackages, below), whose plugin's
// OpEmit calls HostBuild("step-emit", {Word:"system-packages", …}) and whose in-core rendering
// registers here via registerStepEmitter. C1.3 registered the SECOND — builder (stepEmitBuilder,
// below), whose build-emit needs the multi-stage builder render engine (buildStageContext +
// RenderTemplate) that cannot cross the process boundary. C1.4 registered the THIRD —
// local-pkg-install (stepEmitLocalPkgInstall, below); its render logic itself moved to
// deploykit.RenderLocalPkgImageInstall (W3, a pure function of its step argument) — this case
// remains only to thread the shared buildEngineContext alongside the seam's other kinds. C1.5
// registered the FOURTH and RICHEST — op (stepEmitOp, below), whose build-emit drives the full
// Generator.emitTasks per-verb render pipeline (COPY staging, inline-content staging, adjacent
// mkdir/link/setcap coalescing, the act-verb `case "plugin"` seam) that cannot cross the process
// boundary. The seam is
// GENERIC (dispatches by word, no per-word case here), exactly like hostBuilders dispatches by kind.

// stepEmitter renders one host-coupled external step kind's build-context Containerfile fragment
// IN-CORE from the opaque request + the host build-engine context. Registered per step word.
type stepEmitter func(req spec.StepEmitRequest, build buildEngineContext) (string, error)

// stepEmitters maps a step WORD → its in-core fragment renderer. Populated at package-var init
// (before any init(), like hostBuilders / the substrate registries), so lookup is race-free.
// Holds one renderer per relocated host-coupled step kind (C1.2 registered system-packages).
var stepEmitters = map[string]stepEmitter{}

// registerStepEmitter records one host-coupled step kind's in-core fragment renderer. Panics on
// a duplicate (a startup invariant, like registerHostBuilder / registerDeployPreresolver).
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
// fragment as an EmitReply JSON. An unregistered word is a LOUD error (a host-coupled step whose
// in-core renderer was never registered — never a silent empty bake, R4). The buildEngineContext
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

// stepEmitSystemPackages renders the SystemPackages InstallStep's BUILD-context (container-venue)
// Containerfile fragment IN-CORE — the C1.2 relocation of the SystemPackages build-emit off
// deploykit.OCITarget onto the step-emit seam. SystemPackages' build-emit is HOST-COUPLED: it needs the host
// build ENGINE (the DistroDef format templates + RenderTemplate) that cannot cross the process
// boundary, so its serving class:step plugin (candy/plugin-installstep) calls back
// HostBuild("step-emit", …) during OpEmit and this renders the fragment host-side. The render is
// UNCHANGED from the former in-proc build-emit (R3): reconstruct the concrete step from the wire
// view (stepFromView), resolve the box-specific FormatDef via the SAME DistroConfig.FindFormat path
// the host deploy render uses (build.DistroCfg wraps the box-resolved DistroDef — wrapDistroDef),
// and render the format's phase.install.container template. A nil FormatDef is a LOUD error (as the
// former in-proc render was); an empty template for the phase/venue is legitimately nothing to emit.
func stepEmitSystemPackages(req spec.StepEmitRequest, build buildEngineContext) (string, error) {
	var view spec.InstallStepView
	if len(req.Payload) > 0 {
		if err := json.Unmarshal(req.Payload, &view); err != nil {
			return "", fmt.Errorf("decode SystemPackages step view: %w", err)
		}
	}
	step, err := deploykit.StepFromView(view)
	if err != nil {
		return "", err
	}
	s, ok := step.(*deploykit.SystemPackagesStep)
	if !ok {
		return "", fmt.Errorf("step-emit system-packages: view kind %q is not a SystemPackagesStep", view.Kind)
	}
	// The pure render body (FormatDef resolution → phase.install.container template →
	// InstallContext render) lives in sdk/buildkit (P8b — RenderSystemPackagesFragment);
	// core keeps only the deploykit-coupled wire-view → concrete-step reconstruction.
	return buildkit.RenderSystemPackagesFragment(s.Format, s.Phase, s.RawInstallContext, build.DistroCfg)
}

// Register the system-packages step-emitter at package-var init — the FIRST host-coupled step
// kind relocated onto the step-emit seam (C1.2). Its plugin (candy/plugin-installstep) serves the
// OpEmit that calls back HostBuild("step-emit", {Word:"system-packages", …}).
var _ = func() bool { registerStepEmitter("system-packages", stepEmitSystemPackages); return true }()

// stepEmitBuilder renders the Builder InstallStep's BUILD-context (container-venue) Containerfile
// fragment IN-CORE — the C1.3 relocation of the Builder build-emit off deploykit.OCITarget onto the step-emit
// seam. The Builder build-emit is HOST-COUPLED: it needs the host build ENGINE — the builder:
// vocabulary (BuilderConfig, for DETECTION + cache mounts + context inputs), the box UID/GID +
// builder-ref (ResolvedBox), and Generator.buildStageContext to compute the render context — none of
// which can cross the process boundary. So its serving class:step plugin (candy/plugin-installstep)
// calls back HostBuild("step-emit", …) during OpEmit and this renders the fragment host-side. For an
// EXTERNALIZED detection builder (pixi/npm/aur/cargo) the STAGE render itself is kit.BuilderResolve
// (C10 — the SAME render the box-build path + the plugin's OpResolve use, R3, driven off the
// host-computed buildStageContext via deploykit.BuilderResolveInputFrom); a non-externalized builder has no
// build-time multi-stage (a custom builder must be an external_builder plugin). The build engine
// (Generator/BuilderConfig/Box) is threaded on the reverse channel via buildEngineContext (populated
// by the buildEngineContext); a nil BuilderConfig / Box / layer yields the SAME informative
// skip comment the former in-proc render produced (synthetic test paths), and an undefined builder or
// a template error is a LOUD failure (never a silent empty bake, R4).
func stepEmitBuilder(req spec.StepEmitRequest, build buildEngineContext) (string, error) {
	var view spec.InstallStepView
	if len(req.Payload) > 0 {
		if err := json.Unmarshal(req.Payload, &view); err != nil {
			return "", fmt.Errorf("decode Builder step view: %w", err)
		}
	}
	step, err := deploykit.StepFromView(view)
	if err != nil {
		return "", err
	}
	s, ok := step.(*deploykit.BuilderStep)
	if !ok {
		return "", fmt.Errorf("step-emit builder: view kind %q is not a BuilderStep", view.Kind)
	}

	if build.BuilderConfig == nil {
		return fmt.Sprintf("# Builder: %s (layer=%s) — skipped, no BuilderConfig\n",
			s.Builder, s.CandyName), nil
	}
	bDef, ok := build.BuilderConfig.Builder[s.Builder]
	if !ok || bDef == nil {
		return "", fmt.Errorf("builder %q: not defined in BuilderConfig", s.Builder)
	}
	if build.Box == nil {
		return fmt.Sprintf("# Builder: %s (layer=%s) — skipped, no Image context\n",
			s.Builder, s.CandyName), nil
	}

	// candyByName is nil-safe (returns nil for a nil Generator) and carries the remote
	// qualified-key add_candy fallback (bare CandyName → the fully-qualified Candies key).
	layer := build.Generator.candyByName(s.CandyName)
	if layer == nil {
		return fmt.Sprintf("# Builder: %s (layer=%s) — layer not found in scan\n",
			s.Builder, s.CandyName), nil
	}

	// Inline builders (cargo): render the in-candy RUN with the builder's inline context; no
	// separate FROM stage. Switch USER to the image user for the inline builder steps. An
	// EXTERNALIZED inline builder (cargo) renders via kit.BuilderResolve (C10 — the SAME render
	// the box-build path and the plugin's OpResolve use, R3); a custom one via its vocabulary
	// install_template.
	if bDef.Inline {
		ctx := &spec.BuildStageContext{
			LayerStage:  layer.Name,
			UID:         build.Box.UID,
			GID:         build.Box.GID,
			CacheMounts: bDef.CacheMount,
		}
		if externalizedBuilders[s.Builder] {
			reply, err := kit.BuilderResolve(s.Builder, deploykit.BuilderResolveInputFrom(layer.Name, s.Builder, bDef, ctx))
			if err != nil {
				return "", fmt.Errorf("inline builder %s: %w", s.Builder, err)
			}
			return fmt.Sprintf("USER %d\n", build.Box.UID) + reply.InlineFragment, nil
		}
		rendered, err := buildkit.RenderTemplate(s.Builder+"-inline", bDef.InstallTemplate, ctx)
		if err != nil {
			return "", fmt.Errorf("inline builder %s: %w", s.Builder, err)
		}
		return fmt.Sprintf("USER %d\n", build.Box.UID) + rendered, nil
	}

	// Multi-stage builders (pixi/npm/aur): emit the stage via the Generator's buildStageContext
	// helper. A synthetic path without a Generator falls back to an informative comment (the layer
	// lookup above already returned nil for a nil Generator, so this is defensive parity with the
	// former in-proc render).
	if build.Generator == nil {
		return fmt.Sprintf("# Builder: %s (layer=%s) — multi-stage requires Generator; emit skipped\n",
			s.Builder, s.CandyName), nil
	}
	builderRef := ""
	if build.Box.Builder != nil {
		builderRef = build.Box.Builder[s.Builder]
	}
	ctx := build.Generator.buildStageContext(layer, s.Builder, bDef, build.Box, builderRef)
	if ctx == nil {
		return "", fmt.Errorf("buildStageContext returned nil for %s", s.Builder)
	}
	// A multi-stage builder (pixi/npm/aur) renders its stage via kit.BuilderResolve (C10 — the
	// SAME render the box-build path + the plugin's OpResolve use, R3). Only externalized (plugin)
	// builders have a multi-stage; a custom builder must be an external_builder plugin.
	if !externalizedBuilders[s.Builder] {
		return "", fmt.Errorf("multi-stage builder %s is not an externalized plugin builder (a custom builder must be an external_builder plugin)", s.Builder)
	}
	reply, err := kit.BuilderResolve(s.Builder, deploykit.BuilderResolveInputFrom(layer.Name, s.Builder, bDef, ctx))
	if err != nil {
		return "", fmt.Errorf("multi-stage builder %s: %w", s.Builder, err)
	}
	return reply.Stage, nil
}

// Register the builder step-emitter at package-var init — the SECOND host-coupled step kind
// relocated onto the step-emit seam (C1.3). Its plugin (candy/plugin-installstep) serves the OpEmit
// that calls back HostBuild("step-emit", {Word:"builder", …}).
var _ = func() bool { registerStepEmitter("builder", stepEmitBuilder); return true }()

// stepEmitLocalPkgInstall renders the LocalPkgInstall InstallStep's BUILD-context Containerfile
// fragment — the C1.4 relocation of the LocalPkgInstall build-emit off deploykit.OCITarget onto the
// step-emit seam. deploykit.RenderLocalPkgImageInstall (relocated from core, W3) is now a PURE
// function of its step argument (no *Config, no live *Candy graph) — it BUILDS the candy's
// package from LOCAL in-development source on the HOST for a disposable check bed
// (deploykit.BuildLocalPkgOnHost — makepkg / podman, which the compiled-in candy/plugin-installstep
// can do itself; host exec/file-I/O is not a process-boundary concern) and STAGES the built file
// into the per-image build dir (ImageBuildDir). This step-emit case remains ONLY because the
// per-image build ENGINE context (Generator.DevLocalPkg + Box.Name + ImageBuildDir) is threaded
// via buildEngineContext, which still requires the host round-trip for the OTHER step-emit kinds
// sharing this seam (system-packages/builder/op) — reconstruct the concrete step from the wire
// view (stepFromView), then call deploykit.RenderLocalPkgImageInstall: a PRODUCTION box DOWNLOADS
// the published release, a DISPOSABLE bed BUILDS the in-development package and COPYs it in; a
// distro with no localpkg-capable format (LocalPkg==nil) renders nothing. The
// overlay/deploy path never sets DevLocalPkg, so the pod-overlay build-emit takes the production leg.
func stepEmitLocalPkgInstall(req spec.StepEmitRequest, build buildEngineContext) (string, error) {
	var view spec.InstallStepView
	if len(req.Payload) > 0 {
		if err := json.Unmarshal(req.Payload, &view); err != nil {
			return "", fmt.Errorf("decode LocalPkgInstall step view: %w", err)
		}
	}
	step, err := deploykit.StepFromView(view)
	if err != nil {
		return "", err
	}
	s, ok := step.(*deploykit.LocalPkgInstallStep)
	if !ok {
		return "", fmt.Errorf("step-emit local-pkg-install: view kind %q is not a LocalPkgInstallStep", view.Kind)
	}
	dev := build.Generator != nil && build.Generator.DevLocalPkg
	boxName := ""
	if build.Box != nil {
		boxName = build.Box.Name
	}
	return deploykit.RenderLocalPkgImageInstall(s, dev, build.ImageBuildDir, boxName)
}

// Register the local-pkg-install step-emitter at package-var init — the THIRD host-coupled step kind
// relocated onto the step-emit seam (C1.4). Its plugin (candy/plugin-installstep) serves the OpEmit
// that calls back HostBuild("step-emit", {Word:"local-pkg-install", …}).
var _ = func() bool { registerStepEmitter("local-pkg-install", stepEmitLocalPkgInstall); return true }()

// stepEmitOp renders the Op InstallStep's BUILD-context Containerfile fragment IN-CORE — the C1.5
// relocation of the OpStep build-emit off deploykit.OCITarget onto the step-emit seam, the FOURTH host-coupled
// step kind and the RICHEST: the OpStep build-emit drives Generator.emitTasks, the full per-verb
// render pipeline (COPY staging from the layer scratch stage, content-addressed inline-content
// staging under .build/<image>/_inline, adjacent mkdir/link/setcap coalescing, and the act-verb
// `case "plugin"` seam the box build shares). emitTasks needs the host build ENGINE — the scanned
// Candy set (Generator.candyByName), the box UID/GID/Home (ResolvedBox), and the per-image build dir
// + build-context prefix for inline-content staging — none of which can cross the process boundary.
// So its serving class:step plugin (candy/plugin-installstep) calls back HostBuild("step-emit", …)
// during OpEmit and this renders the fragment host-side. The render is UNCHANGED from the former
// in-proc deploykit.OCITarget Op build-emit (R3): reconstruct the *OpStep from the wire view (stepFromView), look the
// candy up by its bare name (candyByName — nil-safe, with the remote qualified-key add_candy
// fallback), and drive the SAME Generator.emitTasks the box build (writeCandySteps→emitTasks) uses,
// for the ONE op the step carries. The build engine (Generator + Box + ImageBuildDir +
// ContextRelPrefix) is threaded on the reverse channel via buildEngineContext (populated by
// the buildEngineContext); the overlay/deploy path is the only build-emit caller (the box
// build never routes an OpStep through deploykit.OCITarget). A synthetic path without a Generator / Box yields
// the SAME informative comment the former in-proc deploykit.OCITarget Op build-emit produced; a candy the scan never saw is a
// LOUD error (never a silent empty bake, R4).
func stepEmitOp(req spec.StepEmitRequest, build buildEngineContext) (string, error) {
	var view spec.InstallStepView
	if len(req.Payload) > 0 {
		if err := json.Unmarshal(req.Payload, &view); err != nil {
			return "", fmt.Errorf("decode Op step view: %w", err)
		}
	}
	step, err := deploykit.StepFromView(view)
	if err != nil {
		return "", err
	}
	s, ok := step.(*deploykit.OpStep)
	if !ok {
		return "", fmt.Errorf("step-emit op: view kind %q is not an OpStep", view.Kind)
	}
	if build.Generator == nil || build.Box == nil {
		kind, _ := s.Op.Kind()
		return fmt.Sprintf("# Task: %s (layer=%s) — no Generator context\n", kind, s.CandyName), nil
	}
	layer := build.Generator.candyByName(s.CandyName)
	if layer == nil {
		return "", fmt.Errorf("task emit: candy %q not found", s.CandyName)
	}
	var b strings.Builder
	if _, err := build.Generator.emitTasks(&b, layer, build.Box, []spec.Op{*s.Op}, build.ImageBuildDir, build.ContextRelPrefix); err != nil {
		return "", err
	}
	return b.String(), nil
}

// Register the op step-emitter at package-var init — the FOURTH host-coupled step kind relocated
// onto the step-emit seam (C1.5). Its plugin (candy/plugin-installstep) serves the OpEmit that calls
// back HostBuild("step-emit", {Word:"op", …}).
var _ = func() bool { registerStepEmitter("op", stepEmitOp); return true }()

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
// The buildEngineContext: an IN-PROC caller (the compiled-in class:step plugin's OpEmit calling
// back for a host-coupled sub-kind) threads `build` via the in-proc reverse channel
// (executorReverseServer) — that path is unchanged. An OUT-OF-PROCESS caller (the overlay candy)
// cannot thread it, so `build` arrives empty + the emitter looks up the cached overlay
// buildEngineContext by Dir (loadOverlayBuildContext, populated by hostBuildOverlay's prep). The
// inner per-word host-coupled calls (system-packages/builder/local-pkg/op) that ociEmitStep's
// spliceClassStepEmit dispatches re-thread this `build` via the in-proc reverse channel it stands
// up, so they get the SAME context without a cache hit.
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
