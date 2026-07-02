package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/opencharly/charly/charly/spec"
)

// pod_deploy_lifecycle.go — the HOST-SIDE lifecycle hook for the EXTERNAL `pod` deploy
// substrate (Design A — the SAME seam vm uses). pod is the DEFAULT substrate: a deployment
// run as a container IMAGE via quadlet/podman. Unlike vm (whose plugin WALKS the plan inside
// the guest), pod bakes its install steps INTO the image at BUILD time — so there is NO
// per-step venue walk. This hook owns the host-only container lifecycle the plugin cannot:
//
//   - PrepareVenue BUILDS the overlay container image HOST-SIDE (the SAME core OCITarget /
//     Generator build engine, in-process — nothing crosses gRPC, exactly as vm's
//     PrepareVenue builds the VM disk host-side). It lifts the prior PodUnifiedTarget.Add
//     body verbatim: NewGenerator + resolve the ResolvedBox from node.Image + the base
//     image's DistroDef + the baseRef CalVer + secret injection + PodDeployTarget.Emit
//     (overlay synthesis, or the deploy-alias tag when there is no add_candy). It returns a
//     host-local ShellExecutor so the generic externalDeployTarget.apply's plugin walk is a
//     clean no-op (plugin-deploy-pod.Invoke walks nothing) and recordVenueLedger no-ops (a
//     pod carries no venue-side ledger — its candies live in the image).
//   - Start/Stop/Status/Logs/Shell shell to the `charly start/stop/status/logs/shell` CLI.
//   - Rebuild = `box build`+`check box`+`bundle add`+`stop`+`config`+`start` (the pod
//     rebuild sequence the `charly update <pod-bed>` R10 fresh-rebuild gate routes through).
//   - PostTeardown = `charly remove` + drop the synthesized <name>-overlay images
//     (keep-image-gated) + ephemeral lifecycle teardown.
//
// It carries NO per-deploy mutable state (a stateless singleton, like vmSubstrateLifecycle)
// — every method re-resolves what it needs from (name, node) and re-loads config itself.
// The bed runner drives a pod bed through the SAME build→config→start→check-live→remove
// path as the in-proc pod (only the `bundle add` overlay-build internally routes through
// this hook now); a pod is NOT an "in-place" external substrate (see check_bed_run.go).

// podSubstrateLifecycle implements substrateLifecycle for the `pod` word.
type podSubstrateLifecycle struct{}

// register at package-var init (before any init(), race-free with the rest of the wiring).
var _ = func() bool {
	registerSubstrateLifecycle("pod", podSubstrateLifecycle{})
	return true
}()

// PrepareVenue builds the overlay container image HOST-SIDE and returns a host-local
// ShellExecutor (the plugin walks nothing; the overlay is baked here). It REQUESTS the build
// through the uniform F10 hostBuilders registry (the "overlay" host-builder — the pod-substrate
// sibling of "image"/"containerfiles"), instead of constructing the PodDeployTarget/OCITarget
// inline: the build ENGINE stays host-side, in-process (runOverlayBuild), and only the
// serializable OverlayBuildRequest crosses the registry seam. The overlay build's LIVE inputs —
// the add_candy overlay plans and, for a nested pod-in-pod overlay, the parent venue executor +
// node (the "venue" the build runs in) — cannot ride a []byte payload, so they are threaded on
// the ctx (the SAME pattern sdk.ContextWithExecutor uses across the reverse channel). A DIRECT
// registry call (not a plugin round-trip like `charly box build`) is correct: PrepareVenue is
// ALREADY the host-side pod lifecycle hook, so the engine it dispatches is host-side too —
// exactly like ensureBuilderImageBuilt calling the host image engine without a reverse-channel
// hop. plans is the add_candy overlay plan set (empty for a pod with no add_candy; the
// external-substrate compileNodePlans skips the primary box plan — the base candies are already
// baked into the base image).
func (podSubstrateLifecycle) PrepareVenue(ctx context.Context, name, dir string, node *BundleNode, plans []*InstallPlan, opts EmitOpts) (DeployExecutor, error) {
	if dir == "" {
		if cwd, err := os.Getwd(); err == nil {
			dir = cwd
		}
	}
	if node == nil {
		tree, err := resolveTreeRoot(dir)
		if err != nil {
			return nil, fmt.Errorf("pod deploy %q: resolve deploy node: %w", name, err)
		}
		n, ok := tree[name]
		if !ok {
			return nil, fmt.Errorf("pod deploy %q: no deploy entry", name)
		}
		node = &n
	}

	// Ephemeral lifecycle hook (FIRST action — panic-safe TTL ordering). Consumes the
	// MERGED node (never a charly.yml re-read).
	registerEphemeralIfMarked(node, name)

	// Request the overlay build via the uniform hostBuilders registry. The registration is a
	// package-var init invariant, so a missing builder is a hard startup bug — surfaced loud.
	fn, ok := hostBuilderFor(overlayBuilderKind)
	if !ok {
		return nil, fmt.Errorf("pod deploy %q: no %q host-builder registered", name, overlayBuilderKind)
	}
	reqJSON, err := marshalJSON(spec.OverlayBuildRequest{
		Dir:              dir,
		DeployName:       name,
		Image:            node.Image,
		Version:          node.Version,
		DryRun:           opts.DryRun,
		AssumeYes:        opts.AssumeYes,
		AllowRepoChanges: opts.AllowRepoChanges,
		AllowRootTasks:   opts.AllowRootTasks,
		WithServices:     opts.WithServices,
	})
	if err != nil {
		return nil, fmt.Errorf("pod deploy %q: marshal overlay request: %w", name, err)
	}
	// Thread the LIVE build inputs (the compiled plans + the nested-venue ParentExec/
	// ParentNode) on the ctx — a live executor cannot ride the []byte request.
	ctx = withOverlayBuildInputs(ctx, &overlayBuildInputs{
		plans:      plans,
		parentExec: opts.ParentExec,
		parentNode: opts.ParentNode,
	})
	resJSON, err := fn(ctx, reqJSON, buildEngineContext{})
	if err != nil {
		return nil, fmt.Errorf("pod deploy %q: overlay build: %w", name, err)
	}
	var reply spec.OverlayBuildReply
	if len(resJSON) > 0 {
		if err := json.Unmarshal(resJSON, &reply); err != nil {
			return nil, fmt.Errorf("pod deploy %q: decode overlay reply: %w", name, err)
		}
	}
	if reply.Error != "" {
		return nil, fmt.Errorf("pod deploy %q: %s", name, reply.Error)
	}

	if !opts.DryRun {
		overlayRef := reply.OverlayRef
		fmt.Printf("Overlay image ready: %s\n", overlayRef)
		fmt.Println("To start the container, run: charly start " + reply.DeployName)
		// Persist the concrete overlay ref so config/start deploy EXACTLY this
		// overlay (carrying the add_candy: layers), instead of re-resolving the
		// base image: short-name by a CalVer sort the overlay alias can lose to
		// the base on a same-minute build (the add_candy-on-pod deploy-resolution
		// quirk). Only when an overlay was actually built — add_candy present, so
		// OverlayImageRef differs from the base; a plain pod persists nothing and
		// config falls back to the base-name resolution. Keyed exactly as config
		// reads it (parseDeployKey → the same box/instance split).
		if overlayRef != "" && overlayRef != reply.BaseImage {
			boxKey, instKey := parseDeployKey(name)
			saveDeployState(boxKey, instKey, SaveDeployStateInput{ResolvedImage: overlayRef})
		}
	}

	// Host-local venue: the overlay is baked host-side; the plugin walks nothing, and
	// recordVenueLedger no-ops on a ShellExecutor (a pod carries no venue-side ledger).
	return ShellExecutor{}, nil
}

// overlayBuilderKind is the F10 hostBuilders key for the pod-overlay build — a generic action
// noun ("build the overlay"), the pod-substrate sibling of "image"/"containerfiles" (build.go)
// and "plugin-binary" (plugin_dispatch_reverse.go). Deliberately NOT a provider WORD (the F11
// uniform-API gate forbids one on this surface — TestNoSinglePluginAPISurface).
const overlayBuilderKind = "overlay"

// overlayBuildInputs carries the LIVE (non-serializable) inputs for the pod-overlay build
// across the F10 hostBuilders registry seam: the compiled InstallPlans and, for a nested
// pod-in-pod overlay, the parent venue executor + node (the venue the overlay `podman build`
// runs in). They cannot cross a []byte specJSON boundary (a live DeployExecutor is not
// serializable), so they ride the ctx — the SAME mechanism sdk.ContextWithExecutor uses to
// thread a live executor across the placement-invisible reverse channel. The serializable
// OverlayBuildRequest carries the scalars; this carries the rest.
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

// overlayBuildInputsFrom reads the live overlay-build inputs from ctx (nil when absent — a
// caller that requested the build with no live inputs, e.g. an empty-plans probe).
func overlayBuildInputsFrom(ctx context.Context) *overlayBuildInputs {
	in, _ := ctx.Value(overlayBuildInputsKey{}).(*overlayBuildInputs)
	return in
}

// hostBuildOverlay is the F10 "overlay" host-builder: it decodes the OverlayBuildRequest
// scalars, reads the live plans + parent venue from the ctx, runs the pod-overlay build engine
// HOST-SIDE in-proc, and returns the opaque OverlayBuildReply. A build FAILURE rides
// OverlayBuildReply.Error (the reply-error convention, like hostBuildImage). The
// buildEngineContext arg is unused: the engine reconstructs Config/ResolvedBox/Candy from
// req.Dir (exactly as the prior inline PrepareVenue body — and runBoxBuild — do).
func hostBuildOverlay(ctx context.Context, specJSON []byte, _ buildEngineContext) ([]byte, error) {
	var req spec.OverlayBuildRequest
	if err := json.Unmarshal(specJSON, &req); err != nil {
		return nil, fmt.Errorf("overlay host-build: decode request: %w", err)
	}
	reply, err := runOverlayBuild(ctx, req, overlayBuildInputsFrom(ctx))
	reply.Error = errString(err)
	return marshalJSON(reply)
}

// runOverlayBuild is the HOST-SIDE pod-overlay build engine behind the "overlay" host-builder.
// It reconstructs the Generator + ResolvedBox + DistroDef from req.Dir (so the overlay's
// OCITarget renders task steps as actual RUN directives with the base image's package format),
// resolves the base image ref, injects candy secrets, and runs PodDeployTarget.Emit — which
// synthesizes the add_candy overlay when present, or tags the deploy-name alias when there is
// none. The engine body is UNCHANGED from the prior inline PrepareVenue construction; only its
// home moved (host-side, in-process — nothing crosses gRPC). The live plans + parent venue
// come from `in` (threaded on the ctx, never serialized).
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

	// Re-load the build vocabulary (distro/builder) for the base-image DistroDef + the
	// overlay BuilderConfig. dispatchNode already registered it via loadConfigForDeploy; the
	// engine re-reads the configs (self-contained, like runBoxBuild re-runs NewGenerator).
	distroCfg, builderCfg, _, err := LoadDefaultBuildConfig(dir)
	if err != nil {
		return spec.OverlayBuildReply{}, fmt.Errorf("load build config: %w", err)
	}

	// The box ref the overlay inherits FROM (the `pod: image:` field). Falls back to the
	// deploy name for a bare deploy (legacy parity with PodUnifiedTarget's NodeName fallback).
	base := req.Image
	if base == "" {
		base = req.DeployName
	}
	tag := req.Version

	// Build a Generator + ResolvedBox so the overlay's OCITarget renders task steps as
	// actual RUN directives (not "no Generator context" comments). Thread the deploy's
	// add_candy: refs into the candy scan (ExtraCandyRefs) — the SAME merged set the
	// compile (compileNodePlans) resolved, carried on the plans' AddCandies provenance
	// (R3) — so the OpStep build-emit's candyByName can resolve each add_candy candy BY NAME.
	// Without this the overlay Generator scanned only the project + box candies, so an add_candy
	// candy carrying a run:/task step (e.g. a remote @github marker layer) failed the
	// overlay build with `task emit: candy "<name>" not found`, baking the add_candy
	// layer into NOTHING. A bare local ref is a no-op in ExtraCandyRefs (addRef gates on
	// IsRemoteCandyRef; the project scan already has it); a remote @github ref joins the
	// fetch (honoring CHARLY_REPO_OVERRIDE), exactly as in compileNodePlans. Empty for a
	// no-add_candy pod (collectOverlayCandies returns nil), so prior behavior is
	// unchanged for base-only deploys.
	gen, _ := NewGenerator(dir, tag, ResolveOpts{ExtraCandyRefs: collectOverlayCandies(plans)})
	var resolvedImg *ResolvedBox
	if gen != nil && gen.Boxes != nil {
		resolvedImg = gen.Boxes[base]
	}

	// Resolve DistroDef from the BASE IMAGE's distro, not the operator host's — the
	// overlay's SystemPackagesSteps render using the base image's package format.
	var podDistroDef *DistroDef
	if resolvedImg != nil && len(resolvedImg.Distro) > 0 {
		podDistroDef = resolveDistroDef(distroCfg, resolvedImg.Distro[0])
	} else {
		podDistroDef = resolveDistroDef(distroCfg, detectHostContext().Distro)
	}

	// Build the BaseImage ref. With CalVer-only resolution, an empty tag resolves to the
	// newest local CalVer so the overlay FROM line gets a real tag (never a trailing colon).
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

	// A nested pod (a dotted deploy path, e.g. "parent.child") flattens to a dot-free
	// container/overlay name — the SAME NestedContainerName mapping the prior dispatcher's
	// PodUnifiedTarget case applied. A top-level pod (no dot) is unchanged.
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

	// Resolve + inject candy secrets so the overlay Containerfile emits `export VAR=VALUE`
	// before each add_candy task body (R3 shared helper). No-op when plans is empty.
	if _, _, serr := prepareCandySecrets(plans, dir); serr != nil {
		return spec.OverlayBuildReply{}, fmt.Errorf("loading candies for secret resolution: %w", serr)
	}

	// Thread ParentExec: when this container is a child of another deployment, the overlay
	// build runs in the parent's venue.
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

// Register the overlay host-builder on the F10 HostBuild seam at package-var init (before any
// init(), like the substrate/preresolver registries + the image/containerfiles builders).
var _ = func() bool { registerHostBuilder(overlayBuilderKind, hostBuildOverlay); return true }()

// ArtifactKey keys candy artifacts under the deploy name (the generic default) — pod has no
// shared-cluster artifact naming like vm's k3s ClusterProfile, so it returns "".
func (podSubstrateLifecycle) ArtifactKey(string, *BundleNode) string { return "" }

// PostApply is a no-op for pod: a pod bakes its candies into the overlay image, and its
// nested children deploy via the bed runner's tree walk AFTER `charly start` (not as an
// in-substrate orchestration like vm's nested pod-in-guest quadlets).
func (podSubstrateLifecycle) PostApply(context.Context, string, string, *BundleNode, DeployExecutor, EmitOpts) error {
	return nil
}

// TeardownExecutor returns nil so the generic Del keeps the ResolveTarget-selected executor
// (the host ShellExecutor). pod records no reverse ops, so the replay is a host-side no-op;
// the real teardown is PostTeardown (`charly remove` + drop overlay).
func (podSubstrateLifecycle) TeardownExecutor(string, *BundleNode) (DeployExecutor, error) {
	return nil, nil
}

// PostTeardown runs the canonical, record-free pod teardown (the SAME body as the prior
// PodUnifiedTarget.Del): delegate container + quadlet + sidecar + charly.yml cleanup to
// `charly remove`, then drop the deploy's synthesized <name>-overlay images (bundle del's
// one extra over `charly remove`, keep-image-gated), and cancel any ephemeral TTL lifecycle.
func (podSubstrateLifecycle) PostTeardown(name string, node *BundleNode, keepImage bool) error {
	if err := runCharlySubcommand("remove", name); err != nil {
		return err
	}
	if !keepImage {
		removeDeployOverlayImages(podDeployEngine(node), name)
	}
	if dcNode, ok := loadDeployConfigForRead("pod bundle-del ephemeral-teardown").LookupKey(name); ok && dcNode.IsEphemeral() {
		if tdErr := TeardownEphemeralLifecycle(&dcNode, name); tdErr != nil {
			fmt.Fprintf(os.Stderr, "warning: ephemeral lifecycle teardown: %v\n", tdErr)
		}
	}
	return nil
}

// Start brings the container deploy up via `charly start`.
func (podSubstrateLifecycle) Start(_ context.Context, name string, _ *BundleNode) error {
	return runCharlySubcommand("start", name)
}

// Stop brings the container deploy down via `charly stop`.
func (podSubstrateLifecycle) Stop(_ context.Context, name string, _ *BundleNode) error {
	return runCharlySubcommand("stop", name)
}

// Status parses `charly status --json` for this deploy's row (the SAME best-effort scan the
// prior PodUnifiedTarget.Status used — avoids coupling to the still-evolving status JSON).
func (podSubstrateLifecycle) Status(_ context.Context, name string, _ *BundleNode) (StatusInfo, error) {
	out, err := captureCharlyStdout("status", "--json")
	if err != nil {
		return StatusInfo{State: "unknown"}, err
	}
	for line := range strings.SplitSeq(out, "\n") {
		if !strings.Contains(line, name) {
			continue
		}
		state := "stopped"
		switch {
		case strings.Contains(line, "running"):
			state = "running"
		case strings.Contains(line, "paused"):
			state = "paused"
		case strings.Contains(line, "crashed"):
			state = "crashed"
		}
		return StatusInfo{
			State:   state,
			Healthy: state == "running",
			Details: map[string]string{"deploy": name},
		}, nil
	}
	return StatusInfo{State: "stopped", Healthy: false}, nil
}

// Logs streams or tails the container's journal via `charly logs`.
func (podSubstrateLifecycle) Logs(_ context.Context, name string, _ *BundleNode, opts LogsOpts) error {
	args := []string{"logs", name}
	if opts.Follow {
		args = append(args, "-f")
	}
	if opts.Tail > 0 {
		args = append(args, "-n", fmt.Sprintf("%d", opts.Tail))
	}
	return runCharlySubcommand(args...)
}

// Shell opens an interactive shell in the container via `charly shell` (or runs cmd).
func (podSubstrateLifecycle) Shell(_ context.Context, name string, _ *BundleNode, cmd []string) error {
	args := make([]string, 0, 2+len(cmd))
	args = append(args, "shell", name)
	args = append(args, cmd...)
	return runCharlySubcommand(args...)
}

// Rebuild follows the pod rebuild sequence (the SAME body as the prior PodUnifiedTarget
// .Rebuild): image rebuild (optional) → image check → deploy add → stop → config (regen
// quadlet) → start. This is the path `charly update <pod-bed>` routes through (the
// disposable bed's fresh-rebuild R10 gate). `charly stop` (not `remove`) preserves operator
// charly.yml config during the brief disruption window.
func (podSubstrateLifecycle) Rebuild(_ context.Context, name string, node *BundleNode, opts RebuildOpts) error {
	baseRef := ""
	if node != nil {
		baseRef = node.Image
	}
	if baseRef == "" {
		baseRef = name
	}

	if opts.DryRun {
		if opts.RebuildImage {
			fmt.Printf("dry-run: charly box build %s\n", baseRef)
			fmt.Printf("dry-run: charly check box %s\n", baseRef)
		}
		fmt.Printf("dry-run: charly bundle add %s\n", name)
		fmt.Printf("dry-run: charly stop %s\n", name)
		fmt.Printf("dry-run: charly config %s\n", name)
		fmt.Printf("dry-run: charly start %s\n", name)
		return nil
	}

	if opts.RebuildImage {
		if err := runCharlySubcommand("box", "build", baseRef); err != nil {
			return fmt.Errorf("charly box build %s: %w", baseRef, err)
		}
		if err := runCharlySubcommand("check", "box", baseRef); err != nil {
			return fmt.Errorf("charly check box %s: %w", baseRef, err)
		}
	}

	if err := runCharlySubcommand("bundle", "add", name); err != nil {
		return fmt.Errorf("charly bundle add %s: %w", name, err)
	}

	_ = runCharlySubcommand("stop", name)

	if err := runCharlySubcommand("config", name); err != nil {
		return fmt.Errorf("charly config %s: %w", name, err)
	}

	if err := runCharlySubcommand("start", name); err != nil {
		return fmt.Errorf("charly start %s: %w", name, err)
	}
	return nil
}

// podDeployEngine returns the container engine for a pod deploy node — node.Engine when
// set, else "podman" (the default). Relocated from the deleted PodUnifiedTarget.engine().
func podDeployEngine(node *BundleNode) string {
	if node != nil && node.Engine != "" {
		return node.Engine
	}
	return "podman"
}

// removeDeployOverlayImages best-effort removes the synthesized <deployName>-overlay images
// (all tags) for a pod deploy. Record-free: it queries the engine for images whose
// repository is "<deployName>-overlay" — the OverlayImageRef naming convention
// (deploy_target_pod.go) — so only the deploy-specific overlays are removed; a shared base
// ref is never named that. Relocated from the deleted unified_targets_pod.go.
func removeDeployOverlayImages(engine, deployName string) {
	out, err := exec.Command(EngineBinary(engine), "images",
		"--filter", "reference="+deployName+"-overlay", "--format", "{{.Repository}}:{{.Tag}}").Output()
	if err != nil {
		return
	}
	for _, ref := range strings.Fields(string(out)) {
		_ = exec.Command(EngineBinary(engine), "rmi", ref).Run()
	}
}
