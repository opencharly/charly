package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/spec/spec"
)

// build_overlay.go — the HOST-SIDE pod-overlay build prep seam (M4 + P11c, SHRUNK further in #55
// step3 3-II). The overlay's render-prepped resolved-project envelope now comes from
// candy/plugin-deploy-pod's OWN InvokeProvider("build","generate",sdk.OpResolve,{Boxes:[base],
// ExtraCandyRefs:overlayCandies}) call — the SAME plugin-side resolveBuildEngine pipeline the main
// build:box/build:generate drive already runs (RDD-proven live: InitSystem/InitDef/BakedMetadata
// populate on this Op, unlike build:project's deliberately-lean resolve) — instead of this host
// seam reconstructing a separate *Generator via the now-DELETED NewGenerator. That plugin-side
// resolveBuildEngine call ALSO already stages remote add_candy: candy sources (runHostFSPrep's
// createRemoteCandyCopies, K3 host-prep move) and resolves per-candy security (spec.CandyModel.Security
// is wire-carried; the candy computes it itself via candyByName(dg.Candies,name).Security(), no host
// fetch needed). What GENUINELY remains host-only here: the base-image metadata a live podman
// executor must read (BaseUser/BaseSecurity/BaseRegistry, via ExtractMetadata + the OCI registry
// label — a sdk-only candy cannot exec podman inspect itself), and re-attaching the LIVE plans +
// parent venue that ride the ctx (overlayBuildInputs, set by the host's own PrepareVenue dispatch,
// never serialized) — plus a minimal buildEngineContext for the "oci-emit-step" step-emitter
// (charly/step_emit_hostbuild.go's stepEmitOCIEmitStep → dispatchOCIStep), which reads ONLY
// build.Box.Name (env.Image) and build.Generator.DevLocalPkg/ExtraCandyRefs (env.DevLocalPkg/
// ExtraCandyRefs — the latter is the RCA'd fix that widens candy/plugin-installstep's OWN
// independent envelope re-fetch for the SAME overlay candies, still required). build.DistroCfg
// itself has no confirmed live reader on the step-emit path today (traced: dispatchOCIStep never
// touches it — the step-emit words that DO care about distro/format read it off their OWN
// candy-side envelope's box.DistroDef, derived from the box's wire-carried .Distro tag, not from
// this host struct) — but the podDistroDef VALUE it wraps still MUST reflect the BASE IMAGE's own
// distro (R1 FIX, caught by team-lead's code reading: the former detectHostContext() fallback used
// the OPERATOR HOST's distro, a live cross-distro bug on any host whose distro differs from the
// base image's — e.g. an Arch/CachyOS host building a Fedora-based overlay). Sourced via a cheap,
// standalone buildkit.ResolveBox for JUST the base box name (no candy scan needed — the base
// image's own declared distro: list is independent of the overlay candies), mirroring exactly what
// the deleted NewGenerator's gen.Boxes[base].Distro gave the OLD code; detectHostContext() is now
// ONLY a last-resort fallback if that lightweight resolve fails.
//
// The candy (plugin-deploy-pod podPrepareVenue) calls this shrunk seam via HostBuild("overlay")
// FIRST (to learn reply.Plans, needed to compute the overlay candy set), THEN makes its own
// render-prepped resolve call, constructs its deploykit.Generator via the shared
// deploykit.NewRenderGeneratorFromProject (unchanged, #67/P11c), renders the overlay Containerfile
// in its own code, and runs podman build + the alias tag via the served executor. The per-step
// Containerfile fragments are rendered HOST-SIDE via the generic "step-emit" host-builder
// (HostBuild("step-emit", "oci-emit-step") → ociEmitStep), unchanged by this cutover.

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
	plans      []*spec.InstallPlan
	parentExec spec.DeployExecutor
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

// hostBuildOverlay is the F10 "overlay" host-builder (P11c — the overlay-BUILD dissolution;
// SHRUNK FURTHER in #55 step3 3-II). The render-prepped resolved-project envelope no longer comes
// from here — candy/plugin-deploy-pod's podPrepareVenue fetches it itself via
// InvokeProvider("build","generate",sdk.OpResolve,...) BEFORE calling this seam. What remains: the
// base image ref + tag, the base-image metadata a live podman executor must read (BaseUser/
// BaseSecurity/BaseRegistry), and re-attaching the live plans + parent venue from ctx
// (overlayBuildInputs, set host-side by PrepareVenue's own dispatch, never serialized) — plus a
// minimal buildEngineContext for the "oci-emit-step" step-emitter (see this file's header for the
// exact fields it still needs and why). A build FAILURE rides OverlayBuildReply.Error.
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
	var plans []*spec.InstallPlan
	var parentExec spec.DeployExecutor
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

	distroCfg, _, _, err := LoadDefaultBuildConfig(dir)
	if err != nil {
		return spec.OverlayBuildReply{}, fmt.Errorf("load build config: %w", err)
	}

	base := req.Image
	if base == "" {
		base = req.DeployName
	}
	tag := req.Version

	// The overlay candy set (add_candy: refs) — threaded into the buildEngineContext below so the
	// class:step OpEmit's BuildEnv.ExtraCandyRefs widens candy/plugin-installstep's OWN independent
	// envelope re-fetch the SAME way (dispatchOCIStep, charly/oci_step_emit.go) — this is the ONLY
	// consumer of ExtraCandyRefs on this path now; the remote-candy STAGING itself already ran as
	// part of the candy's OWN InvokeProvider("build","generate",...) resolve (runHostFSPrep's
	// createRemoteCandyCopies, K3 host-prep move) before this seam is ever reached.
	overlayCandies := collectOverlayCandies(plans)

	// DistroDef sourced from the BASE IMAGE's own resolved distro tag, NOT the operator host's
	// (R1 FIX, caught by team-lead's code reading: an overlay layers ON the base image, so its
	// per-step distro-format rendering must target the BASE's distro — this host runs Arch/CachyOS
	// while check-pod-overlay's base is Fedora, so the former detectHostContext() fallback was a
	// live cross-distro bug waiting for a reader, not merely a latent one). See
	// resolveOverlayBaseDistroDef's own doc for the cheap resolve + fallback shape.
	podDistroDef := resolveOverlayBaseDistroDef(dir, base, distroCfg)

	var baseRef string
	switch {
	case tag != "":
		baseRef = base + ":" + tag
	default:
		if resolved, rerr := kit.ResolveNewestLocalCalVer("podman", base); rerr == nil && resolved != "" {
			baseRef = resolved
		} else {
			baseRef = base
		}
	}

	deployName := req.DeployName
	if strings.Contains(deployName, ".") {
		deployName = kit.NestedContainerName(deployName)
	}

	secretEnv, _, serr := resolveCandySecrets(plans, dir)
	if serr != nil {
		return spec.OverlayBuildReply{}, fmt.Errorf("loading candies for secret resolution: %w", serr)
	}
	deploykit.InjectSecretsIntoPlans(plans, secretEnv)

	// Base-image metadata (ExtractMetadata + the registry label) — the candy emits the post-overlay
	// USER restore + the security LABEL from these (it cannot run podman inspect itself). The
	// overlay build always uses podman (the build runs in the parent venue via the served executor,
	// but metadata extraction is host-side).
	var baseUser string
	var baseSecurity *spec.Security
	baseRegistry := readImageRegistry("podman", baseRef)
	if baseMeta, merr := deploykit.ExtractMetadata("podman", baseRef); merr == nil && baseMeta != nil {
		baseUser = baseMeta.User
		sec := baseMeta.Security
		baseSecurity = &sec
	}

	// Serialize the live plans as InstallPlanViews — the candy decodes them via
	// spec.PlanFromView + walks the overlay candies' steps. The live InstallPlan carries
	// concrete steps; the InstallPlanView is the wire form (R3 — the same step-IR round-trip the
	// external deploy walk uses).
	plansView := make([]spec.InstallPlanView, 0, len(plans))
	for _, p := range plans {
		if p == nil {
			continue
		}
		plansView = append(plansView, spec.WireView(p))
	}

	// Cache the overlay buildEngineContext for the "oci-emit-step" step-emitter. The candy's
	// deploykit.OCITarget.EmitStepOp seam calls HostBuild("step-emit", {Word:"oci-emit-step",
	// Payload: OCIEmitStepParams{Dir, StepView, PlanView}, Distros}) per step; the emitter looks up
	// this cache by Dir + calls ociEmitStep, which reads ONLY build.Box.Name (env.Image) and
	// build.Generator.DevLocalPkg/ExtraCandyRefs (env.DevLocalPkg/ExtraCandyRefs — the RCA'd fix
	// that widens candy/plugin-installstep's own independent envelope re-fetch for these SAME
	// overlay candies) — traced in full in charly/oci_step_emit.go's dispatchOCIStep. Neither field
	// needs the deleted NewGenerator's full resolve: Box is a bare Name-only synthetic (its OTHER
	// fields are unread on this path), Generator is a bare literal carrying just the two scalars
	// dispatchOCIStep reads. ImageBuildDir/ContextRelPrefix = the overlay build dir (relative to
	// the project root, so emitWrite's inline COPY prefix resolves, matching the full build's
	// contextRelPrefix = buildDir convention).
	overlayBuildDir := filepath.Join(".build", "overlay-"+deployName)
	build := buildEngineContext{
		DistroCfg:        spec.WrapDistroDef(podDistroDef),
		Generator:        &Generator{ExtraCandyRefs: overlayCandies},
		Box:              &buildkit.ResolvedBox{ResolvedBox: spec.ResolvedBox{Name: base}},
		ImageBuildDir:    overlayBuildDir,
		ContextRelPrefix: overlayBuildDir,
	}
	storeOverlayBuildContext(dir, &build)

	return spec.OverlayBuildReply{
		BaseImage:     baseRef,
		DeployName:    deployName,
		Plans:         plansView,
		BaseUser:      baseUser,
		BaseSecurity:  baseSecurity,
		BaseRegistry:  baseRegistry,
		CalVer:        ComputeCalVer(),
		ParentVolumes: parentVolumes,
	}, nil
}

// Register the overlay host-builder on the F10 HostBuild seam at package-var init.
var _ = func() bool {
	registerHostBuilder(overlayBuilderKind, typedHostBuilder(overlayBuilderKind, hostBuildOverlay))
	return true
}()

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

// resolveOverlayBaseDistroDef resolves the pod-overlay's per-step distro-format vocabulary from the
// BASE IMAGE's OWN declared distro tag (R1 FIX — team-lead's code reading: the overlay layers ON
// the base image, so system-packages/format rendering must target the BASE's distro, never the
// operator HOST's — an Arch/CachyOS host building a Fedora-based overlay is a live, not merely
// latent, cross-distro bug). A cheap, standalone buildkit.ResolveBox for JUST the named base box —
// no candy scan, no ExtraCandyRefs needed, since a box's own distro: list is independent of which
// candies later get overlaid onto it — mirrors exactly what the deleted NewGenerator's
// gen.Boxes[base].Distro gave the OLD code, without needing the deleted full-resolve machinery.
// Falls back to the operator host's own distro ONLY if that lightweight resolve fails (e.g. an
// unresolvable/synthetic base name) — the same defensive floor the OLD code had no equivalent for,
// since NewGenerator's full resolve either succeeded or the whole build failed loudly; here a
// best-effort fallback is preferable to hard-failing the overlay prep over a formatting nicety.
func resolveOverlayBaseDistroDef(dir, base string, distroCfg *spec.DistroConfig) *spec.ResolvedDistro {
	if cfg, cerr := LoadConfig(dir); cerr == nil {
		RegisterBuildVocabulary(distroCfg)
		if bkopts, operr := buildkitOptsWithVocab(dir, loaderkit.ResolveOpts{DistroCfg: distroCfg}); operr == nil {
			if resolvedBase, rerr := buildkit.ResolveBox(cfg, base, "", dir, bkopts); rerr == nil && resolvedBase != nil && len(resolvedBase.Distro) > 0 {
				return resolveDistroDef(distroCfg, resolvedBase.Distro[0])
			}
		}
	}
	return resolveDistroDef(distroCfg, detectHostContext().Distro)
}

// collectOverlayCandies returns the set of candy names declared as add_candy in any plan's meta.
// Union all plans' AddCandies slices. Pure (no core state); the candy keeps its own copy because
// it cannot import charly core (R3 — cross-module reuse is fine; the two modules cannot import
// each other). Used by the overlay prep (hostBuildOverlay) to scope the Generator's ExtraCandyRefs
// + to read each overlay candy's Security() core-side.
func collectOverlayCandies(plans []*spec.InstallPlan) []string {
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
