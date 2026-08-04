package main

// builder_venue.go — the buildEngineContext DATA envelope. The VENUE-AGNOSTIC BuilderStep
// execution path this file used to carry (runVenueBuilderStep/runVenueHomeArtifactBuilder,
// builderStepImage, venueBuilderTarName) took a buildEngineContext parameter only to close over
// its Cfg/ProjectDir fields when building the injected image-resolve/ensure closures
// (resolveImageRefForEnsure/dispatchBuildEnsure) — the SAME shape deploykit.BuildDepPkgsOnHost
// already took directly. Relocated to sdk/deploykit/venue_builder.go (#118 coneB-p8bremainder),
// taking resolveImage/ensureImage as explicit parameters instead of the buildEngineContext
// struct; the caller (plugin_executor_reverse.go's RunHostStep) builds the two closures from its
// own buildEngineContext and passes them in — mirroring how it already called BuildDepPkgsOnHost.
//
// buildEngineContext ITSELF stays core: it carries *Config/*Generator/*buildkit.ResolvedBox
// (core-only types) and is threaded across a dozen already-floor host_build_*.go seams
// (unified_targets.go, build_overlay.go, host_build_pod_config.go, provider_checkenv.go,
// bundle_from_box_cmd.go, plugin_executor_reverse.go) — a generic per-invoke DATA envelope no
// sdk plugin can hold itself (it names core project-loader types), same class as those seams'
// own floor justification.

import (
	"github.com/opencharly/spec/spec"
)

// buildEngineContext is the host-ENGINE context the reverse channel carries so the
// host-served RunHostStep leg can run the in-core machinery a HOST-ENGINE step kind needs:
// a BuilderStep's host build (dispatchBuildEnsure + BuilderRun need the project Config + dir
// to resolve a short / namespace-qualified builder image and to fall back to a local
// `charly box build`), and a SystemPackagesStep's host package-install render (the format's
// phase.install.host template lives in the resolved DistroConfig). The deploy lifecycle
// supplies it (the DeployContext's Cfg + Dir + DistroCfg for an external deploy substrate;
// the Local/VM target's own Cfg + ProjectDir + DistroCfg for an external `run: plugin:`
// step). The ENGINE itself is never carried across the process boundary — only this
// descriptor is.
type buildEngineContext struct {
	Cfg        *spec.Config
	ProjectDir string
	// DistroCfg is the resolved distro: vocabulary the SystemPackagesStep host render
	// (deploykit.RenderHostPackageCommand) needs to look up the format's phase.install.host
	// template. Zero for an Invoke whose plan has no SystemPackagesStep.
	DistroCfg *spec.DistroConfig

	// The following are populated ONLY by the pod-overlay BUILD-emit path (the
	// buildEngineContext). They no longer feed an in-core renderer directly (the former
	// HOST-COUPLED system-packages/builder/local-pkg-install/op step-emitters — C1.2-C1.5 — were
	// relocated onto candy/plugin-installstep's OWN "resolved-project"-built deploykit.Generator,
	// K5-Unit-6b): dispatchOCIStep (charly/oci_step_emit.go) reads Generator/Box to populate
	// the class:step ops.OpEmit's spec.BuildEnv scalars (Image=Box.Name, DevLocalPkg=Generator.DevLocalPkg)
	// so the plugin can render without a per-render host round-trip. They are zero for every
	// deploy-leg buildEngineContext — the Builder DEPLOY leg is runVenueBuilderStep and the
	// LocalPkgInstall DEPLOY leg is deploykit.ExecLocalPkgInstall (separate host-engine paths
	// driven via RunHostStep), which read none of them.
	Generator *Generator
	Box       *spec.ResolvedBox
	// ImageBuildDir is the per-image (pod-overlay) build dir — rides the class:step ops.OpEmit's
	// BuildEnv.ImageBuildDir so candy/plugin-installstep's emitLocalPkgInstall can stage a
	// dev-mode locally-built package the SAME way deploykit's renderLocalPkgImageDevInstall did. It
	// is buildEngineContext.ImageBuildDir, NOT Generator.BuildDir (the overlay build dir differs
	// from the project .build root). Zero for every deploy-leg context.
	ImageBuildDir string
	// ContextRelPrefix is the build-context-relative prefix for staged inline content — rides the
	// class:step ops.OpEmit's BuildEnv.ContextRelPrefix so candy/plugin-installstep's emitOp can pass it
	// to dg.EmitTasks, staging a write: op's content-addressed COPY source under the correct
	// .build/<image>/_inline path. It is buildEngineContext.ContextRelPrefix. Zero for every
	// deploy-leg context.
	ContextRelPrefix string
}
