package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/opencharly/sdk/spec"
)

// build_overlay.go — the HOST-SIDE pod-overlay build ENGINE (M4). The pod deploy LIFECYCLE moved
// out of core to candy/plugin-deploy-pod, but the overlay build engine STAYS core (it drives the
// Generator/OCITarget/PodDeployTarget, which never cross the module boundary). The externalized
// plugin reaches it over the reverse channel via HostBuild("overlay"): the plugin's OpPrepareVenue
// calls Executor.HostBuild("overlay", spec.OverlayBuildRequest{scalars}); the host runs this engine
// in-proc (the LIVE plans + parent venue are re-attached host-side via the reverse server's live
// inputs, never serialized) and returns the OverlayBuildReply. Same engine the compiled-in pod
// lifecycle called directly — only its caller (now the plugin, over the wire) changed.

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
	parentNode *BundleNode
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

// hostBuildOverlay is the F10 "overlay" host-builder: decode the OverlayBuildRequest scalars, read
// the live plans + parent venue from the ctx, run the pod-overlay build engine HOST-SIDE in-proc,
// return the opaque OverlayBuildReply (a build FAILURE rides OverlayBuildReply.Error).
func hostBuildOverlay(ctx context.Context, specJSON []byte, _ buildEngineContext) ([]byte, error) {
	var req spec.OverlayBuildRequest
	if err := json.Unmarshal(specJSON, &req); err != nil {
		return nil, fmt.Errorf("overlay host-build: decode request: %w", err)
	}
	reply, err := runOverlayBuild(ctx, req, overlayBuildInputsFrom(ctx))
	reply.Error = errString(err)
	return marshalJSON(reply)
}

// runOverlayBuild is the HOST-SIDE pod-overlay build engine. It reconstructs the Generator +
// ResolvedBox + DistroDef from req.Dir, resolves the base image ref, injects candy secrets, and
// runs PodDeployTarget.Emit — synthesizing the add_candy overlay when present, or tagging the
// deploy-name alias when there is none. The live plans + parent venue come from `in`.
func runOverlayBuild(_ context.Context, req spec.OverlayBuildRequest, in *overlayBuildInputs) (spec.OverlayBuildReply, error) {
	dir := req.Dir
	if dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return spec.OverlayBuildReply{}, err
		}
		dir = cwd
	}

	var (
		plans      []*InstallPlan
		parentExec DeployExecutor
		parentNode *BundleNode
	)
	if in != nil {
		plans = in.plans
		parentExec = in.parentExec
		parentNode = in.parentNode
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

	// A Generator + ResolvedBox so the overlay's OCITarget renders task steps as actual RUN
	// directives. Thread the deploy's add_candy: refs into the candy scan (ExtraCandyRefs) so the
	// OpStep build-emit's candyByName resolves each add_candy candy BY NAME.
	gen, _ := NewGenerator(dir, tag, ResolveOpts{ExtraCandyRefs: collectOverlayCandies(plans)})
	var resolvedImg *ResolvedBox
	if gen != nil && gen.Boxes != nil {
		resolvedImg = gen.Boxes[base]
	}

	// DistroDef from the BASE IMAGE's distro (its package format), not the operator host's.
	var podDistroDef *DistroDef
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

	tgt := &PodDeployTarget{
		DeployName:    deployName,
		BaseImage:     baseRef,
		DistroDef:     podDistroDef,
		BuilderConfig: builderCfg,
		Generator:     gen,
		Box:           resolvedImg,
	}

	if _, _, serr := prepareCandySecrets(plans, dir); serr != nil {
		return spec.OverlayBuildReply{}, fmt.Errorf("loading candies for secret resolution: %w", serr)
	}

	if parentExec != nil {
		tgt.Executor = parentExec
	}

	opts := EmitOpts{
		DryRun:           req.DryRun,
		AssumeYes:        req.AssumeYes,
		AllowRepoChanges: req.AllowRepoChanges,
		AllowRootTasks:   req.AllowRootTasks,
		WithServices:     req.WithServices,
		ParentExec:       parentExec,
		ParentNode:       parentNode,
	}
	if err := tgt.Emit(plans, opts); err != nil {
		return spec.OverlayBuildReply{}, fmt.Errorf("overlay build: %w", err)
	}

	return spec.OverlayBuildReply{
		OverlayRef: tgt.OverlayImageRef(),
		BaseImage:  tgt.BaseImage,
		DeployName: deployName,
	}, nil
}

// Register the overlay host-builder on the F10 HostBuild seam at package-var init.
var _ = func() bool { registerHostBuilder(overlayBuilderKind, hostBuildOverlay); return true }()

// podDeployEngine returns the container engine for a pod deploy node — node.Engine when set, else
// "podman" (the default). Used by the overlay-image teardown.
func podDeployEngine(node *BundleNode) string {
	if node != nil && node.Engine != "" {
		return node.Engine
	}
	return "podman"
}

