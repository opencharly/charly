package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
	"github.com/opencharly/sdk/vmshared"
)

// The var-substitution + user-spec render helpers, the inline-content stager, and
// the per-verb Containerfile-line emitters all live in sdk/deploykit (tasks_emit.go);
// every caller (here and in check_kit_adapter.go / host_build_construct_step.go) references
// deploykit.TaskKnownNames / deploykit.ResolveUserSpec / deploykit.EmitCmd / etc.
// directly (K3 ZERO-ALIASES dissolution).

// --- Orchestrator ---

// toDeploykit constructs the sdk/deploykit render Generator from this core
// Generator's resolved state, wiring the host-coupled seams (P8). The render
// engine lives in deploykit; core RESOLVES + wires the Provider-registry seam.
func (g *Generator) toDeploykit() *deploykit.Generator {
	if g.dkGen != nil {
		return g.dkGen
	}
	dg := deploykit.NewRenderGenerator()
	dg.Dir = g.Dir
	dg.Candies = g.Candies
	dg.Tag = g.Tag
	dg.Boxes = g.Boxes
	// Config + InitConfig: the RESOLVE-side inputs the host render-prep pass
	// (dg.RenderPrepAll/RenderPrepBox, K3-U3) reads to fill the per-box build-render
	// caches. The plugin-build render path (NewRenderGeneratorFromProject) never
	// runs render-prep, so it leaves these nil and reads the caches from the envelope.
	dg.Config = g.Config
	dg.InitConfig = g.InitConfig
	dg.BuildDir = g.BuildDir
	dg.Containerfiles = g.Containerfiles
	dg.GlobalOrder = g.GlobalOrder
	dg.RequestedBoxes = g.RequestedBoxes
	dg.DevLocalPkg = g.DevLocalPkg
	// EmitPluginOp: the ONLY host seam the render needs — dispatch a non-command
	// plugin verb UNIFORMLY through Invoke(OpEmit) via the core Provider registry.
	// No package-main concrete-type assert: a state-provision verb self-declares its
	// act shell via EmitReply.ActScript (see invokeVerbBuildEmit). Registry resolve
	// stays core (the plugin-render path uses InvokeProvider(OpEmit) directly).
	dg.EmitPluginOp = func(op *spec.Op, img *buildkit.ResolvedBox) (string, bool, error) {
		prov, ok := providerRegistry.ResolveVerb(op.Plugin)
		if !ok {
			return "", false, fmt.Errorf("run: plugin verb %q is not registered (an external plugin not connected at build time?)", op.Plugin)
		}
		return invokeVerbBuildEmit(context.Background(), prov, op, img)
	}
	dg.CollectBoxPorts = func(boxName string) ([]string, error) {
		return deploykit.CollectBoxPorts(g.Config, g.Candies, boxName)
	}
	// ValidateEgress/ValidateTextEgress: left unwired here — this toDeploykit() Generator's ONLY
	// surviving caller (resolved_project_host.go's fillNamespacedBoxes) calls RenderPrepBox only,
	// which never reaches Generate()/EmitTraefikRouteStage (confirmed dead by call-graph trace,
	// egress.go dissolution, coneB-buildtail).
	// RenderService: the init-cluster service materialization crosses to
	// candy/plugin-init (OpResolve) + egress-gates host-side. All arg/return types
	// are spec aliases, so the core func satisfies the seam field directly.
	dg.RenderService = RenderService
	// RewriteHeaderCopyForRemote: left unwired here — same dead-in-this-path finding as
	// ValidateEgress/EmitBakedPlugins above (its only caller, EmitInitFragmentStages, runs in
	// Generate()'s per-box render loop, never RenderPrepBox).
	// writeCandySteps seams: the inline-builder registry resolve (builder-emit
	// cluster, stays core) and the localpkg image install. ExternalizedBuilders is
	// the registry fact selecting the branch. RenderLocalPkgImageInstall itself
	// moved to deploykit (W3, pure function of its step argument) — wired directly,
	// no core closure needed.
	dg.ExternalizedBuilders = externalizedBuilders
	dg.RenderLocalPkgImageInstall = deploykit.RenderLocalPkgImageInstall
	dg.ResolveInlineBuilder = g.resolveInlineBuilderSeam
	// Builder-cluster registry seams (K3-A): the multi-stage BUILDER render moved to
	// deploykit (builders_render.go); its ONLY host coupling — the provider registry
	// (on-demand connect + ResolveBuilder + OpResolve Invoke) — is wired here. Error
	// strings are formatted in the seam closures so they stay byte-exact.
	dg.EnsureBuildersConnected = func(detected []string) error {
		return ensureBuildersConnected(context.Background(), g.Config, g.Dir, detected)
	}
	dg.ResolveDetectionBuilderStage = g.resolveDetectionBuilderStageSeam
	dg.ResolveExternalBuilderStage = g.resolveExternalBuilderStageSeam
	// EmitBakedPlugins: left unwired here — this toDeploykit() Generator's ONLY surviving
	// caller (resolved_project_host.go's fillNamespacedBoxes) calls RenderPrepBox only, which
	// never reads EmitBakedPlugins (K3 build-tail move, coneB-buildtail: the real render path
	// wires deploykit.EmitBakedPlugins directly, no host round-trip — see
	// render_generator_from_project.go).
	// CollectBoxVolume: the volume-aggregate seam for data-image label emission.
	// Wraps the core CollectBoxVolume (reads the live Config + Candy graph). Used by
	// deploykit.Generator.generateDataImageContainerfile (#67 render-DRIVE move).
	dg.CollectBoxVolume = func(boxName, home string) ([]deploykit.VolumeMount, error) {
		return deploykit.CollectBoxVolume(g.Config, g.Candies, boxName, home, nil)
	}
	g.dkGen = dg
	return dg
}

// resolveDetectionBuilderStageSeam is the core impl wired onto deploykit's
// ResolveDetectionBuilderStage seam: resolve the externalized detection-builder provider
// from the registry (its "not connected" error byte-preserved) and Invoke its OpResolve
// leg (resolveBuilderStage). deploykit builds the render input (BuildStageContext +
// BuilderResolveInputFrom) and passes it here; the registry resolve + Invoke stays core.
func (g *Generator) resolveDetectionBuilderStageSeam(builderName string, in spec.BuilderResolveInput, img *buildkit.ResolvedBox) (spec.BuilderResolveReply, error) {
	var zero spec.BuilderResolveReply
	prov, ok := providerRegistry.ResolveBuilder(builderName)
	if !ok {
		return zero, fmt.Errorf("detection builder %q is externalized but its plugin is not connected (a plugin-load gap?)", builderName)
	}
	return resolveBuilderStage(prov, builderName, in, img)
}

// resolveExternalBuilderStageSeam is the core impl wired onto deploykit's
// ResolveExternalBuilderStage seam: resolve the `external_builder:`-selected out-of-tree
// provider (its not-registered + compiled-in + resolve errors byte-preserved), assert it
// is an EXTERNAL grpcProvider, and Invoke its OpResolve leg (resolveExternalBuilder, the
// minimal candy-name-only input). Registry-coupled, stays core.
func (g *Generator) resolveExternalBuilderStageSeam(word, candyName string, img *buildkit.ResolvedBox) (spec.BuilderResolveReply, error) {
	var zero spec.BuilderResolveReply
	prov, ok := providerRegistry.ResolveBuilder(word)
	if !ok {
		return zero, fmt.Errorf("candy %q: external_builder %q is not a registered builder (an external plugin not connected at build time?)", candyName, word)
	}
	// Only an EXTERNAL out-of-process builder (a *grpcProvider) drives this build-time
	// OpResolve path; reject any compiled-in provider (defensive — no in-proc builder exists
	// today). NOTE: the four detection-builders (pixi/cargo/npm/aur) are ALSO external
	// grpcProviders now, but they serve only the DEPLOY-time OpCollectContext/OpReverse legs and
	// are SELECTED BY DETECTION (their detect-files / aur: section via the embedded builder:
	// vocabulary), never by external_builder:. Mis-selecting one here passes this type-assert but
	// then fails LOUDLY at resolveExternalBuilder's OpResolve Invoke (the plugin rejects the op).
	if _, isExternal := prov.(*grpcProvider); !isExternal {
		return zero, fmt.Errorf("candy %q: external_builder %q resolves to a compiled-in builder, not an external plugin", candyName, word)
	}
	reply, err := resolveExternalBuilder(prov, word, candyName, img)
	if err != nil {
		return zero, fmt.Errorf("candy %q: external_builder %q resolve: %w", candyName, word, err)
	}
	return reply, nil
}

// resolveInlineBuilderSeam is the core impl wired onto deploykit's
// ResolveInlineBuilder seam: connect + OpResolve an externalized INLINE builder,
// returning its C10 InlineFragment (or a per-failure error, byte-preserved). The
// builder-emit cluster (ensureBuildersConnected + registry ResolveBuilder +
// resolveBuilderStage) is registry-coupled and stays core.
func (g *Generator) resolveInlineBuilderSeam(candyName, bName string, bDef *vmshared.BuilderDef, ctx *spec.BuildStageContext, img *buildkit.ResolvedBox) (string, error) {
	layer := g.Candies[candyName]
	if err := ensureBuildersConnected(context.Background(), g.Config, g.Dir, []string{bName}); err != nil {
		return "", fmt.Errorf("candy %q: connect inline builder %q: %w", candyName, bName, err)
	}
	prov, ok := providerRegistry.ResolveBuilder(bName)
	if !ok {
		return "", fmt.Errorf("candy %q: inline builder %q is externalized but its plugin is not connected", candyName, bName)
	}
	in := deploykit.BuilderResolveInputFrom(layer.GetName(), bName, bDef, ctx)
	reply, err := resolveBuilderStage(prov, bName, in, img)
	if err != nil {
		return "", fmt.Errorf("candy %q: inline builder %q resolve: %w", candyName, bName, err)
	}
	if strings.TrimSpace(reply.InlineFragment) == "" {
		return "", fmt.Errorf("candy %q: inline builder %q returned an empty OpResolve inline fragment", candyName, bName)
	}
	return reply.InlineFragment, nil
}

// invokeVerbBuildEmit renders a plugin verb's BUILD-context Containerfile contribution
// via a UNIFORM Invoke(OpEmit) — placement-agnostic above the registry (in-proc for a
// builtin, over go-plugin gRPC for an external connected by the build-time plugin connect
// seam in NewGenerator), with NO package-main concrete-type assert. The provider receives
// the FULL op as op.Params (a state-provision act reads SHARED #Op modifiers — mode/content —
// beyond plugin_input) + a spec.BuildEnv descriptor as op.Env, and returns a spec.EmitReply:
// a state-provision verb sets ActScript=true and Fragment=the act shell (RUN-wrapped by the
// caller via EmitCmd — the former ProvisionActor branch), any other verb returns a verbatim
// Containerfile Fragment (ActScript=false) spliced as-is. An empty non-act fragment is a loud
// error (a runtime-/deploy-only capability wrongly asked to build-emit; never bake nothing, R4).
// The build-time half of the operator-authorized build-time plugin execution.
func invokeVerbBuildEmit(ctx context.Context, prov Provider, op *spec.Op, img *buildkit.ResolvedBox) (string, bool, error) {
	params, err := marshalJSON(op)
	if err != nil {
		return "", false, fmt.Errorf("run: plugin verb %q build-emit: marshal op: %w", op.Plugin, err)
	}
	var distros []string
	if img != nil {
		distros = img.Tags
	}
	env, err := marshalJSON(spec.BuildEnv{Distros: distros})
	if err != nil {
		return "", false, fmt.Errorf("run: plugin verb %q build-emit: marshal build env: %w", op.Plugin, err)
	}
	res, err := prov.Invoke(ctx, &Operation{Reserved: op.Plugin, Op: OpEmit, Params: params, Env: env})
	if err != nil {
		return "", false, fmt.Errorf("run: plugin verb %q build-emit: %w", op.Plugin, err)
	}
	var reply spec.EmitReply
	if res != nil && len(res.JSON) > 0 {
		if uerr := json.Unmarshal(res.JSON, &reply); uerr != nil {
			return "", false, fmt.Errorf("run: plugin verb %q build-emit: decode OpEmit reply: %w", op.Plugin, uerr)
		}
	}
	if !reply.ActScript && strings.TrimSpace(reply.Fragment) == "" {
		return "", false, fmt.Errorf("run: plugin verb %q returned an empty OpEmit fragment — it has no build-context act (a runtime-only verb in a build run: step, or a deploy-only step declaring emits without an OpEmit fragment? use context: [runtime] / set emits=false)", op.Plugin)
	}
	return reply.Fragment, reply.ActScript, nil
}

// invokeOpEmitFragmentOpt is the OpEmit → EmitReply → Fragment path for the build-context
// external-STEP emit (ociEmitStep, F-STEP-EMIT — the pod-overlay deploykit.OCITarget's
// compiler-emitted-step build-emit). It Invokes the provider's OpEmit with the already-marshalled
// params (a step's opaque Payload) and the caller-supplied spec.BuildEnv descriptor, decodes the
// EmitReply, and returns the Containerfile fragment. (The build-context VERB emit is
// invokeVerbBuildEmit above, which decodes EmitReply.ActScript itself for the uniform
// state-provision act path — P8b.) ctx MAY carry an in-proc reverse channel
// (sdk.ContextWithExecutor) so a HOST-COUPLED step plugin can call back HostBuild during its
// OpEmit; a PURE step plugin ignores it and returns the fragment directly.
// allowEmpty controls the empty-fragment guard: false fails LOUDLY on an empty
// fragment — a runtime-/deploy-only capability wrongly asked to build-emit; true permits an empty
// fragment, used by deploykit.OCITarget for a COMPILER-EMITTED typed step whose render is legitimately empty
// for a given instance (an empty shell snippet, a packaged service with no overrides + enable=false,
// a custom service with no unit text — exactly the cases the former the former in-core emit* returned nothing).
// env carries the caller-populated spec.BuildEnv descriptor — a plain verb emit sets only Distros;
// the pod-overlay dispatch forward (dispatchOCIStep, charly/oci_step_emit.go) additionally sets
// Image/DevLocalPkg/ImageBuildDir/ContextRelPrefix so a HOST-COUPLED step word
// (system-packages/builder/local-pkg-install/op) can render its fragment directly against its OWN
// "resolved-project"-built deploykit.Generator, with NO extra host round-trip beyond this ONE
// Invoke every word already receives.
func invokeOpEmitFragmentOpt(ctx context.Context, prov Provider, word string, params []byte, buildEnv spec.BuildEnv, allowEmpty bool) (string, error) {
	env, err := marshalJSON(buildEnv)
	if err != nil {
		return "", fmt.Errorf("marshal build env: %w", err)
	}
	res, err := prov.Invoke(ctx, &Operation{Reserved: word, Op: OpEmit, Params: params, Env: env})
	if err != nil {
		return "", err
	}
	var reply spec.EmitReply
	if res != nil && len(res.JSON) > 0 {
		if err := json.Unmarshal(res.JSON, &reply); err != nil {
			return "", fmt.Errorf("decode OpEmit reply: %w", err)
		}
	}
	if !allowEmpty && strings.TrimSpace(reply.Fragment) == "" {
		return "", fmt.Errorf("plugin %q returned an empty OpEmit fragment — it has no build-context act (a runtime-only verb in a build run: step, or a deploy-only step declaring emits without an OpEmit fragment? use context: [runtime] / set emits=false)", word)
	}
	return reply.Fragment, nil
}
