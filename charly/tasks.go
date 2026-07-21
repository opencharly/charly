package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// The var-substitution + user-spec render helpers, the inline-content stager, and
// the per-verb Containerfile-line emitters all live in sdk/deploykit (tasks_emit.go);
// every caller (here and in check_kit_adapter.go / install_build_act.go) references
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
	dg.BuildDir = g.BuildDir
	dg.Containerfiles = g.Containerfiles
	dg.GlobalOrder = g.GlobalOrder
	dg.RequestedBoxes = g.RequestedBoxes
	dg.DevLocalPkg = g.DevLocalPkg
	// EmitPluginOp: the ONLY host seam the render needs — dispatch a non-command
	// plugin verb through the core Provider registry (a ProvisionActor act-shell,
	// else the OpEmit fragment). Error strings preserved byte-exact.
	dg.EmitPluginOp = func(op *spec.Op, img *buildkit.ResolvedBox) (string, bool, error) {
		prov, ok := providerRegistry.ResolveVerb(op.Plugin)
		if !ok {
			return "", false, fmt.Errorf("run: plugin verb %q is not registered (an external plugin not connected at build time?)", op.Plugin)
		}
		if actor, isActor := prov.(ProvisionActor); isActor {
			script, sok := actor.RenderProvisionScript(op, img.Tags)
			if !sok {
				return "", false, fmt.Errorf("run: plugin verb %q is not act-capable (ProvisionActor declined)", op.Plugin)
			}
			return script, true, nil
		}
		frag, ferr := emitPluginFragment(prov, op, img)
		if ferr != nil {
			return "", false, fmt.Errorf("run: plugin verb %q build-emit: %w", op.Plugin, ferr)
		}
		return frag, false, nil
	}
	dg.CollectBoxPorts = func(boxName string) ([]string, error) {
		return CollectBoxPorts(g.Config, g.Candies, boxName)
	}
	dg.ValidateEgress = ValidateEgress
	// ValidateTextEgress: the rendered-Containerfile text gate (kind "rendered_text", mode
	// "text") — the deploykit writeContainerfile calls it instead of the bytes ValidateEgress
	// (#67 render-DRIVE move). Wraps core validateTextEgress.
	dg.ValidateTextEgress = validateTextEgress
	// RenderService: the init-cluster service materialization crosses to
	// candy/plugin-init (OpResolve) + egress-gates host-side. All arg/return types
	// are spec aliases, so the core func satisfies the seam field directly.
	dg.RenderService = RenderService
	// RewriteHeaderCopyForRemote: host-fs materialization of a remote build-config
	// asset referenced by a stage_header_copy COPY line (stays core).
	dg.RewriteHeaderCopyForRemote = g.rewriteHeaderCopyForRemote
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
	// EmitBakedPlugins: the S0 baked-plugin BUILD-side seam — bake each composing
	// candy's bake_plugin binaries into the final image. The host closure is the
	// existing emitBakedPlugins (stays core: host-builds plugin binaries). Used by
	// deploykit.Generator.generateContainerfile (#67 render-DRIVE move).
	dg.EmitBakedPlugins = g.emitBakedPlugins
	// CollectBoxVolume: the volume-aggregate seam for data-image label emission.
	// Wraps the core CollectBoxVolume (reads the live Config + Candy graph). Used by
	// deploykit.Generator.generateDataImageContainerfile (#67 render-DRIVE move).
	dg.CollectBoxVolume = func(boxName, home string) ([]deploykit.VolumeMount, error) {
		return CollectBoxVolume(g.Config, g.Candies, boxName, home, nil)
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
func (g *Generator) resolveInlineBuilderSeam(candyName, bName string, bDef *BuilderDef, ctx *spec.BuildStageContext, img *buildkit.ResolvedBox) (string, error) {
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

// emitPluginFragment renders a plugin verb's BUILD-context Containerfile fragment
// via the provider's OpEmit Invoke — placement-agnostic above the registry (in-proc
// for a builtin, over go-plugin gRPC for an external connected by the build-time
// plugin connect seam in NewGenerator). The plugin receives its plugin_input as
// op.Params and a spec.BuildEnv descriptor as op.Env, and returns a spec.EmitReply
// whose Fragment is spliced verbatim into the generated Containerfile. The build-time
// half of the operator-authorized build-time plugin execution.
func emitPluginFragment(prov Provider, op *spec.Op, img *buildkit.ResolvedBox) (string, error) {
	params, err := marshalJSON(op.PluginInput)
	if err != nil {
		return "", fmt.Errorf("marshal plugin_input: %w", err)
	}
	var distros []string
	if img != nil {
		distros = img.Tags
	}
	return invokeOpEmitFragment(context.Background(), prov, op.Plugin, params, spec.BuildEnv{Distros: distros})
}

// invokeOpEmitFragment is the ONE OpEmit → EmitReply → empty-guard → Fragment path (R3),
// shared by the build-context VERB emit (emitPluginFragment, via emitTasks) AND the
// build-context external-STEP emit (ociEmitStep, F-STEP-EMIT). It Invokes
// the provider's OpEmit with the already-marshalled params (a verb's plugin_input, or a
// step's opaque Payload) and the caller-supplied spec.BuildEnv descriptor, decodes the EmitReply,
// and returns the Containerfile fragment — failing LOUDLY on an empty fragment (a runtime-/deploy-only
// capability wrongly asked to build-emit; never bake nothing silently, R4). ctx MAY carry an
// in-proc reverse channel (sdk.ContextWithExecutor) so a HOST-COUPLED plugin can call back
// HostBuild during its OpEmit; a PURE plugin ignores it and returns the fragment directly.
func invokeOpEmitFragment(ctx context.Context, prov Provider, word string, params []byte, env spec.BuildEnv) (string, error) {
	return invokeOpEmitFragmentOpt(ctx, prov, word, params, env, false)
}

// invokeOpEmitFragmentOpt is the OpEmit → EmitReply → Fragment core shared by the guarding
// invokeOpEmitFragment and the pod-overlay deploykit.OCITarget's compiler-emitted-step build-emit (R3).
// allowEmpty controls the empty-fragment guard: false (the default) fails LOUDLY on an empty
// fragment — a runtime-/deploy-only capability wrongly asked to build-emit; true permits an empty
// fragment, used by deploykit.OCITarget for a COMPILER-EMITTED typed step whose render is legitimately empty
// for a given instance (an empty shell snippet, a packaged service with no overrides + enable=false,
// a custom service with no unit text — exactly the cases the former the former in-core emit* returned nothing).
// env carries the caller-populated spec.BuildEnv descriptor — a plain verb emit sets only Distros;
// the class:step emit (ociSpliceClassStepEmit) additionally sets Image/DevLocalPkg/ImageBuildDir/
// ContextRelPrefix so a HOST-COUPLED step word (system-packages/builder/local-pkg-install/op) can
// render its fragment directly against its OWN "resolved-project"-built deploykit.Generator, with
// NO extra host round-trip beyond this ONE Invoke every word already receives.
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
