package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/opencharly/sdk/spec"
)

// build_overlay.go — the HOST-SIDE pod-overlay build PREP+RESOLVE seam (M4 + P11c). The pod deploy
// LIFECYCLE + the overlay BUILD RENDER both moved out of core to candy/plugin-deploy-pod; what STAYS
// core is the prep+resolve M-seam: reconstruct the core *Generator (with the deploy's add_candy refs
// as ExtraCandyRefs), resolve the base image ref + distro + init + base-image metadata, stage remote
// candy copies (host-fs), project an overlay-scoped *spec.ResolvedProject, serialize the live plans,
// cache the buildEngineContext for the "oci-emit-step" step-emitter, and return the envelope. The
// externalized plugin reaches this prep over the reverse channel via HostBuild("overlay"); the candy
// (plugin-deploy-pod podPrepareVenue) consumes the envelope, constructs a deploykit.Generator via
// the shared deploykit.NewRenderGeneratorFromProject, renders the overlay Containerfile in its own
// code, and runs podman build + the alias tag via the served executor. The per-step Containerfile
// fragments are rendered HOST-SIDE via the generic "step-emit" host-builder (HostBuild("step-emit",
// "oci-emit-step") → ociEmitStep). The LIVE plans + parent venue ride the ctx (overlayBuildInputs),
// re-attached host-side by the reverse server, never serialized.

// overlayBuilderKind is the F10 hostBuilders key for the pod-overlay build — a generic action noun,
// the pod-substrate sibling of "image"/"containerfiles"/"plugin-binary"/"cli". Deliberately NOT a
// provider WORD (the F11 uniform-API gate forbids one — TestNoSinglePluginAPISurface).
const overlayBuilderKind = "overlay"

// overlayBuildInputs carries the LIVE (non-serializable) inputs for the pod-overlay build across the
// F10 hostBuilders registry seam: the compiled InstallPlans and, for a nested pod-in-pod overlay,
// the parent venue executor + node. They cannot cross a []byte specJSON boundary (a live
// DeployExecutor is not serializable), so they ride the ctx. The externalized pod lifecycle plugin's
// HostBuild("overlay") receives them re-attached host-side by the reverse server (the proxy's
// PrepareVenue set them on the Invoke ctx).
type overlayBuildInputs struct {
	plans      []*InstallPlan
	parentExec DeployExecutor
	parentNode *spec.BundleNode
}

type overlayBuildInputsKey struct{}

// withOverlayBuildInputs attaches the live overlay-build inputs to ctx.
func withOverlayBuildInputs(ctx context.Context, in *overlayBuildInputs) context.Context {
	return context.WithValue(ctx, overlayBuildInputsKey{}, in)
}

// overlayBuildInputsFrom reads the live overlay-build inputs from ctx (nil when absent).
func overlayBuildInputsFrom(ctx context.Context) *overlayBuildInputs {
	in, _ := ctx.Value(overlayBuildInputsKey{}).(*overlayBuildInputs)
	return in
}

// hostBuildOverlay is the F10 "overlay" host-builder (P11c — the overlay-BUILD dissolution):
// it shrinks to PREP+RESOLVE only. It reconstructs the core *Generator (with the deploy's
// add_candy refs as ExtraCandyRefs), resolves the base image ref + distro + the overlay init
// system + base-image metadata (ExtractMetadata), stages remote candy copies (host-fs),
// projects an overlay-scoped *spec.ResolvedProject, serializes the live plans, caches the
// buildEngineContext for the "oci-emit-step" step-emitter, and returns the envelope. The candy
// (candy/plugin-deploy-pod podPrepareVenue) consumes this envelope, constructs a
// deploykit.Generator via the shared deploykit.NewRenderGeneratorFromProject, renders the overlay
// Containerfile IN ITS OWN CODE, and runs podman build + the deploy-name alias tag via the served
// executor. Each per-step Containerfile fragment is rendered HOST-SIDE via the generic "step-emit"
// host-builder (HostBuild("step-emit", {Word:"oci-emit-step", …})), which looks up the cached
// buildEngineContext by dir + calls ociEmitStep (the full provider-registry dispatch — byte-identical
// to the former in-core ociEmitStep). The live plans + parent venue come from the ctx
// (overlayBuildInputs); the parent venue is re-attached host-side by the reverse server, never
// serialized. A build FAILURE rides OverlayBuildReply.Error.
//
//nolint:gocyclo // envelope assembler — the prep+resolve+project+cache+envelope arms; one branch per projection step (mirrors projectResolvedProjectWithBoxes).
func hostBuildOverlay(ctx context.Context, req spec.OverlayBuildRequest, _ buildEngineContext) (spec.OverlayBuildReply, error) {
	dir := req.Dir
	if dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return spec.OverlayBuildReply{}, err
		}
		dir = cwd
	}

	// The LIVE plans + parent venue (a non-serializable DeployExecutor for a nested pod-in-pod)
	// ride the ctx, re-attached host-side by the reverse server. The candy never sees the live
	// executor directly: the plans are serialized into the envelope (Plans), the parent venue
	// drives the candy's served executor (the host already threads it onto the candy's Invoke ctx),
	// and the parent node's bind-mount volumes are carried as ParentVolumes so the candy's
	// translateHostPathToVenue maps host paths → venue paths for the nested podman build.
	var plans []*InstallPlan
	var parentExec DeployExecutor
	var parentNode *spec.BundleNode
	if in := overlayBuildInputsFrom(ctx); in != nil {
		plans = in.plans
		parentExec = in.parentExec
		parentNode = in.parentNode
	}
	_ = parentExec
	var parentVolumes []spec.DeployVolume
	if parentNode != nil {
		parentVolumes = parentNode.Volume
	}

	distroCfg, builderCfg, _, err := LoadDefaultBuildConfig(dir)
	if err != nil {
		return spec.OverlayBuildReply{}, fmt.Errorf("load build config: %w", err)
	}

	base := req.Image
	if base == "" {
		base = req.DeployName
	}
	tag := req.Version

	// A Generator + ResolvedBox so the overlay's per-step render (ociEmitStep) renders task steps
	// as actual RUN directives. Thread the deploy's add_candy: refs into the candy scan
	// (ExtraCandyRefs) so the OpStep build-emit's candyByName resolves each add_candy candy BY NAME.
	overlayCandies := collectOverlayCandies(plans)
	gen, _ := NewGenerator(dir, tag, ResolveOpts{ExtraCandyRefs: overlayCandies})
	var resolvedImg *ResolvedBox
	if gen != nil && gen.Boxes != nil {
		resolvedImg = gen.Boxes[base]
	}

	// DistroDef from the BASE IMAGE's distro (its package format), not the operator host's.
	var podDistroDef *spec.ResolvedDistro
	if resolvedImg != nil && len(resolvedImg.Distro) > 0 {
		podDistroDef = resolveDistroDef(distroCfg, resolvedImg.Distro[0])
	} else {
		podDistroDef = resolveDistroDef(distroCfg, detectHostContext().Distro)
	}

	var baseRef string
	switch {
	case tag != "":
		baseRef = base + ":" + tag
	default:
		if resolved, rerr := ResolveNewestLocalCalVer("podman", base); rerr == nil && resolved != "" {
			baseRef = resolved
		} else {
			baseRef = base
		}
	}

	deployName := req.DeployName
	if strings.Contains(deployName, ".") {
		deployName = NestedContainerName(deployName)
	}

	if _, _, serr := prepareCandySecrets(plans, dir); serr != nil {
		return spec.OverlayBuildReply{}, fmt.Errorf("loading candies for secret resolution: %w", serr)
	}

	// Stage REMOTE add_candy candies' source trees into .build/_candy/<name>.<version>/ — host-fs
	// materialization a sdk-only candy cannot do. The candy's FROM scratch COPY references these.
	if gen != nil {
		if err := gen.createRemoteCandyCopies(); err != nil {
			return spec.OverlayBuildReply{}, fmt.Errorf("staging remote overlay candies: %w", err)
		}
	}

	// Base-image metadata (ExtractMetadata + the registry label) — the candy emits the post-overlay
	// USER restore + the security LABEL from these (it cannot run podman inspect itself). The
	// overlay build always uses podman (the build runs in the parent venue via the served executor,
	// but metadata extraction is host-side).
	var baseUser string
	var baseSecurity *spec.Security
	baseRegistry := readImageRegistry("podman", baseRef)
	if baseMeta, merr := ExtractMetadata("podman", baseRef); merr == nil && baseMeta != nil {
		baseUser = baseMeta.User
		sec := baseMeta.Security
		baseSecurity = &sec
	}

	// Per-overlay-candy security — the CandyModel interface the candy builds its deploykit.Generator
	// from has NO Security() method, so the candy cannot read overlay-candy security itself; the
	// host prep reads gen.candyByName(name).Security() core-side + carries it. The candy's
	// renderOverlaySecurityLabel merges each entry on top of BaseSecurity + emits the LABEL directive
	// (mirrors the former in-core renderOverlaySecurityLabel). SecurityConfig == Security
	// (alias), so the carried *Security is the SAME type the former in-core render marshalled.
	overlayCandySecurity := map[string]*spec.Security{}
	if gen != nil {
		for _, name := range overlayCandies {
			layer := gen.candyByName(name)
			if layer == nil {
				continue
			}
			if sec := layer.Security(); sec != nil {
				overlayCandySecurity[name] = sec
			}
		}
	}

	// Resolve the overlay init system (InitConfig.ResolveInitSystem is core-only — the candy cannot
	// call it) with the SAME candy order + preferred-init the former in-core pod-overlay render
	// .renderOverlayServices used (base.Candy + overlayCandies, preferred = the box's InitSystem as
	// NewGenerator left it), so the candy's renderOverlayServices — which reads
	// dg.Boxes[base].InitSystem/.InitDef (re-attached by NewSpecResolvedBox from the envelope) — is
	// byte-faithful. Set on resolvedImg BEFORE projecting so the envelope carries it.
	if gen != nil && gen.InitConfig != nil && resolvedImg != nil {
		candyOrder := append(append([]string{}, resolvedImg.Candy...), overlayCandies...)
		initName, initDef := gen.InitConfig.ResolveInitSystem(gen.Candies, candyOrder, resolvedImg.InitSystem)
		resolvedImg.InitSystem = initName
		resolvedImg.InitDef = initDef
	}

	// Project the overlay-scoped resolved-project envelope — the SAME projection the box-build
	// build-prep uses (projectResolvedProjectWithBoxes). gen.Boxes (with the overlay init) are the
	// pre-resolved boxes; loadProjectForResolve is called with the add_candy refs as ExtraCandyRefs
	// so lp.layers (the candy scan) includes them → rp.CandyModels includes the add_candy candies →
	// the candy's deploykit.Generator.Candies has them (candyByName + HasInit resolve).
	var rp *spec.ResolvedProject
	lp, lperr := loadProjectForResolve(dir, ResolveOpts{ExtraCandyRefs: overlayCandies}, nil)
	if lperr != nil {
		return spec.OverlayBuildReply{}, fmt.Errorf("loading project for overlay envelope: %w", lperr)
	}
	if !lp.empty {
		var initCfg *InitConfig
		if gen != nil {
			initCfg = gen.InitConfig
		} else {
			initCfg = lp.initCfg
		}
		rp, err = projectResolvedProjectWithBoxes(lp.cfg, lp.layers, lp.uf, lp.distroCfg, lp.builderCfg, initCfg, dir, lp.version, ResolveOpts{ExtraCandyRefs: overlayCandies}, nil, gen.Boxes)
		if err != nil {
			return spec.OverlayBuildReply{}, fmt.Errorf("projecting overlay resolved-project envelope: %w", err)
		}
		if gen != nil {
			rp.GlobalOrder = gen.GlobalOrder
		}
		rp.ExternalizedBuilders = externalizedBuilders
	} else {
		rp = &spec.ResolvedProject{}
	}

	// Serialize the live plans as InstallPlanViews — the candy decodes them via
	// deploykit.PlanFromView + walks the overlay candies' steps. The live InstallPlan carries
	// concrete steps; the InstallPlanView is the wire form (R3 — the same step-IR round-trip the
	// external deploy walk uses).
	plansView := make([]spec.InstallPlanView, 0, len(plans))
	for _, p := range plans {
		if p == nil {
			continue
		}
		plansView = append(plansView, planWireView(p))
	}

	// Cache the overlay buildEngineContext for the "oci-emit-step" step-emitter. The candy's
	// deploykit.OCITarget.EmitStepOp seam calls HostBuild("step-emit", {Word:"oci-emit-step",
	// Payload: OCIEmitStepParams{Dir, StepView, PlanView}, Distros}) per step; the emitter looks up
	// this cache by Dir + calls ociEmitStep (the SAME single source of truth the in-core
	// ociEmitStep delegated to). The build context mirrors the former in-core
	// overlayOCITarget: DistroCfg/Generator/BuilderConfig/Box + ImageBuildDir/
	// ContextRelPrefix = the overlay build dir (relative to the project root, so emitWrite's inline
	// COPY prefix resolves, matching the full build's contextRelPrefix = buildDir convention).
	overlayBuildDir := filepath.Join(".build", "overlay-"+deployName)
	build := buildEngineContext{
		DistroCfg:        wrapDistroDef(podDistroDef),
		Generator:        gen,
		BuilderConfig:    builderCfg,
		Box:              resolvedImg,
		ImageBuildDir:    overlayBuildDir,
		ContextRelPrefix: overlayBuildDir,
	}
	storeOverlayBuildContext(dir, &build)
	// ALSO cache the overlay core *Generator in renderGenCache so the render-seam "render-service"
	// handler (loadRenderGen(dir)) finds it — the candy's dg.GenerateInitFragments calls back
	// HostBuild("render-seam", "render-service", …) for each service fragment, + the host renders it
	// via the cached Generator (the SAME #67 render-seam the box build uses).
	if gen != nil {
		renderGenCache.Store(dir, gen)
	}

	return spec.OverlayBuildReply{
		BaseImage:            baseRef,
		DeployName:           deployName,
		ResolvedProject:      rp,
		Plans:                plansView,
		BaseUser:             baseUser,
		BaseSecurity:         baseSecurity,
		BaseRegistry:         baseRegistry,
		CalVer:               ComputeCalVer(),
		OverlayCandySecurity: overlayCandySecurity,
		ParentVolumes:        parentVolumes,
	}, nil
}

// Register the overlay host-builder on the F10 HostBuild seam at package-var init.
var _ = func() bool {
	registerHostBuilder(overlayBuilderKind, typedHostBuilder(overlayBuilderKind, hostBuildOverlay))
	return true
}()

// podDeployEngine returns the container engine for a pod deploy node — node.Engine when set, else
// "podman" (the default). Used by the overlay-image teardown.
func podDeployEngine(node *spec.BundleNode) string {
	if node != nil && node.Engine != "" {
		return node.Engine
	}
	return "podman"
}

// overlayBuildContextCache holds the live overlay buildEngineContext per project dir for the
// "step-emit" host-builder's "oci-emit-step" emitter (P11c). Populated by hostBuildOverlay's
// prep+resolve (the overlay core *Generator + DistroDef/BuilderConfig/Box/ImageBuildDir/
// ContextRelPrefix, constructed with the deploy's add_candy refs as ExtraCandyRefs); read by the
// "oci-emit-step" step-emitter when an OUT-OF-PROCESS caller (candy/plugin-deploy-pod) invokes
// HostBuild("step-emit", {Word:"oci-emit-step", …}) — the live buildEngineContext cannot cross the
// wire, so the candy passes only the Dir key + the host looks up the cached context. One entry per
// dir per process — mirrors renderGenCache (the box-build's per-dir Generator cache for its
// render-seam). The render-seam host-builder (hostBuildRenderSeam) reads the overlay core
// *Generator from renderGenCache[dir] (the overlay prep stores it there too, so the render-seam
// handlers — RenderService for GenerateInitFragments — work for the overlay unchanged); this cache
// holds the FULL buildEngineContext the step-emit emitter needs (DistroCfg/BuilderConfig/Box/
// ImageBuildDir/ContextRelPrefix alongside the Generator).
var overlayBuildContextCache sync.Map

// storeOverlayBuildContext caches the overlay buildEngineContext for dir (the "oci-emit-step"
// emitter reads it). A no-op for an empty dir / nil build (defensive).
func storeOverlayBuildContext(dir string, build *buildEngineContext) {
	if dir == "" || build == nil {
		return
	}
	overlayBuildContextCache.Store(dir, build)
}

// loadOverlayBuildContext returns the cached overlay buildEngineContext for dir, or nil when absent.
func loadOverlayBuildContext(dir string) *buildEngineContext {
	if dir == "" {
		return nil
	}
	v, ok := overlayBuildContextCache.Load(dir)
	if !ok {
		return nil
	}
	return v.(*buildEngineContext)
}

// collectOverlayCandies returns the set of candy names declared as add_candy in any plan's meta.
// Union all plans' AddCandies slices. Pure (no core state); the candy keeps its own copy because
// it cannot import charly core (R3 — cross-module reuse is fine; the two modules cannot import
// each other). Used by the overlay prep (hostBuildOverlay) to scope the Generator's ExtraCandyRefs
// + to read each overlay candy's Security() core-side.
func collectOverlayCandies(plans []*InstallPlan) []string {
	seen := make(map[string]bool)
	var out []string
	for _, p := range plans {
		for _, n := range p.AddCandies {
			if !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	return out
}

// readImageRegistry reads the ai.opencharly.registry OCI label from an image — the registry prefix
// the deploy-name alias tag carries so deployment-name-keyed commands (`charly config/start`)
// resolve the image when deploy-name != image-name. Used by the overlay prep (hostBuildOverlay) to
// carry BaseRegistry in the envelope (the candy cannot run podman inspect itself).
func readImageRegistry(engine, imageRef string) string {
	out, err := exec.Command(engine, "inspect", "--format", "{{index .Config.Labels \"ai.opencharly.registry\"}}", imageRef).CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
