package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/opencharly/spec/container"
	specexec "github.com/opencharly/spec/exec"
	"github.com/opencharly/spec/spec"
)

// build_overlay.go — the HOST-SIDE pod-overlay build prep seam (M4 + P11c, SHRUNK further in #55
// step3 3-II, then the #55 coneB-br2 cutover shed its last sdk/deploykit imports). The overlay's
// render-prepped resolved-project envelope now comes from candy/plugin-deploy-pod's OWN
// InvokeProvider("build","generate",sdk.OpResolve,{Boxes:[base],ExtraCandyRefs:overlayCandies}) call
// — the SAME plugin-side resolveBuildEngine pipeline the main build:box/build:generate drive already
// runs (RDD-proven live: InitSystem/InitDef/BakedMetadata populate on this Op, unlike
// build:project's deliberately-lean resolve) — instead of this host seam reconstructing a separate
// *Generator via the now-DELETED NewGenerator. That plugin-side resolveBuildEngine call ALSO
// already stages remote add_candy: candy sources (runHostFSPrep's createRemoteCandyCopies, K3
// host-prep move) and resolves per-candy security (spec.CandyModel.Security is wire-carried; the
// candy computes it itself via candyByName(dg.Candies,name).Security(), no host fetch needed).
// candy/plugin-deploy-pod's buildOverlay ALSO resolves the overlay candies' secret_requires:/
// secret_accepts: env PLUGIN-SIDE (deploykit.SelectCandiesForPlans + ResolveSecretForCandy over
// deploykit.CredentialAccessViaExecutor + InjectSecretsIntoPlans, mirroring
// candy/plugin-bundle/secrets_artifacts.go) — the former host-side resolveCandySecrets +
// InjectSecretsIntoPlans + the enc.go coreCredentialAccess adapter + layer_secrets.go are DELETED
// in the coneB-br2 cutover (the deploykit import this file carried for them is gone).
//
// What GENUINELY remains host-only here: the base-image metadata a live podman executor must read
// (BaseUser/BaseSecurity/BaseRegistry, via ExtractMetadata + the OCI registry label — a sdk-only
// candy cannot exec podman inspect itself), and re-attaching the LIVE plans + parent venue that
// ride the ctx (overlayBuildInputs, set by the host's own PrepareVenue dispatch, never serialized)
// — plus a minimal buildEngineContext for the "oci-emit-step" step-emitter
// (charly/step_emit_hostbuild.go's stepEmitOCIEmitStep → dispatchOCIStep), which reads ONLY
// build.Box.Name (env.Image) and build.Generator.DevLocalPkg/ExtraCandyRefs (env.DevLocalPkg/
// ExtraCandyRefs — the latter widens candy/plugin-installstep's OWN independent envelope re-fetch
// for the SAME overlay candies). build.DistroCfg is nil on the overlay path: the step-emit words
// that care about distro/format read their OWN candy-side envelope's box.DistroDef (derived from
// the box's wire-carried .Distro tag — the base-distro render is plugin-side via
// dg.Boxes[env.Image].DistroDef in candy/plugin-installstep's emitSystemPackages, K5-A/C1.2), NOT
// this host struct; the former resolveOverlayBaseDistroDef host resolve (deploykit.ResolveSpecBox
// for the base .Distro) was DEAD on the step-emit path and is DELETED (R1 doc-divergence: the prior
// comment claimed it "MUST reflect the base distro" but the trace proved no live reader —
// dispatchOCIStep never touched it). nil is the safe value (safer than the former
// detectHostContext() operator-host fallback that was the original cross-distro bug;
// detectHostContext + resolveDistroDef were DELETED in #55 coneB-br2 Group 2 — their sole caller
// resolveOverlayBaseDistroDef above was dropped, deadening them; the deploy compile uses the
// plugin's own twin candy/plugin-bundle/dispatch.go detectHostContext).
//
// The candy (plugin-deploy-pod podPrepareVenue) calls this shrunk seam via HostBuild("overlay")
// FIRST (to learn reply.Plans, needed to compute the overlay candy set), THEN makes its own
// render-prepped resolve call, constructs its deploykit.Generator via the shared
// deploykit.NewRenderGeneratorFromProject (unchanged, #67/P11c), resolves + injects the overlay
// candies' secrets plugin-side, renders the overlay Containerfile in its own code, and runs
// podman build + the alias tag via the served executor. The per-step Containerfile fragments are
// rendered HOST-SIDE via the generic "step-emit" host-builder (HostBuild("step-emit",
// "oci-emit-step") → ociEmitStep), unchanged by this cutover.

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

	var baseRef string
	switch {
	case tag != "":
		baseRef = base + ":" + tag
	default:
		if resolved, rerr := container.ResolveNewestLocalCalVer("podman", base); rerr == nil && resolved != "" {
			baseRef = resolved
		} else {
			baseRef = base
		}
	}

	deployName := req.DeployName
	if strings.Contains(deployName, ".") {
		deployName = specexec.NestedContainerName(deployName)
	}

	// Base-image metadata (ExtractMetadata + the registry label) — the candy emits the post-overlay
	// USER restore + the security LABEL from these (it cannot run podman inspect itself). The
	// overlay build always uses podman (the build runs in the parent venue via the served executor,
	// but metadata extraction is host-side).
	var baseUser string
	var baseSecurity *spec.Security
	baseRegistry := readImageRegistry("podman", baseRef)
	if baseMeta, merr := container.ExtractMetadata("podman", baseRef); merr == nil && baseMeta != nil {
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
		DistroCfg:        spec.WrapDistroDef(nil),
		Generator:        &Generator{ExtraCandyRefs: overlayCandies},
		Box:              &spec.ResolvedBox{Name: base},
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
// prep (the overlay core *Generator + Box/ImageBuildDir/ContextRelPrefix, constructed with the
// deploy's add_candy refs as ExtraCandyRefs; DistroCfg is nil on the overlay path — see this
// file's header); read by the "oci-emit-step" step-emitter when an OUT-OF-PROCESS caller
// (candy/plugin-deploy-pod) invokes HostBuild("step-emit", {Word:"oci-emit-step", …}) — the live
// buildEngineContext cannot cross the wire, so the candy passes only the Dir key + the host looks
// up the cached context. One entry per dir per process — mirrors renderGenCache (the box-build's
// per-dir Generator cache for its render-seam). The render-seam host-builder (hostBuildRenderSeam)
// reads the overlay core *Generator from renderGenCache[dir] (the overlay prep stores it there
// too, so the render-seam handlers — RenderService for GenerateInitFragments — work for the
// overlay unchanged); this cache holds the buildEngineContext the step-emit emitter needs
// (Box/ImageBuildDir/ContextRelPrefix alongside the Generator — dispatchOCIStep reads only
// build.Box.Name + build.Generator.DevLocalPkg/ExtraCandyRefs, never DistroCfg).
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
