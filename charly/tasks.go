package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// P8 shims — the var-substitution + user-spec render helpers moved to
// sdk/deploykit (tasks_emit.go). charly keeps these var-aliases so the
// validate.go / check / bundle-add / install callers stay unchanged until
// they relocate.
var (
	taskKnownNames       = deploykit.TaskKnownNames
	taskUnresolvedRefs   = deploykit.TaskUnresolvedRefs
	resolveUserSpec      = deploykit.ResolveUserSpec
	taskSubstAutoExports = deploykit.TaskSubstAutoExports
	taskSubstPath        = deploykit.TaskSubstPath
)

// stageInlineContent → deploykit.StageInlineContent (P8 shim).
var stageInlineContent = deploykit.StageInlineContent

// P8 shims — the per-verb Containerfile-line emitters moved to sdk/deploykit
// (tasks_emit.go). charly keeps these var-aliases so callers outside tasks.go
// (emitWrite←deploy_target_pod, emitVarsEnv←generate, emitCmd←checkspec/…) stay
// unchanged until they relocate.
var (
	emitVarsEnv     = deploykit.EmitVarsEnv
	emitMkdirBatch  = deploykit.EmitMkdirBatch
	emitCopy        = deploykit.EmitCopy
	emitWrite       = deploykit.EmitWrite
	emitLinkBatch   = deploykit.EmitLinkBatch
	emitSetcapBatch = deploykit.EmitSetcapBatch
	taskCacheMounts = deploykit.TaskCacheMounts
)

// emitDownload → deploykit.EmitDownload (P8 shim).
var emitDownload = deploykit.EmitDownload

// emitCmd → deploykit.EmitCmd (P8 shim).
var emitCmd = deploykit.EmitCmd

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
	dg.Candies = candyModelMap(g.Candies)
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
	dg.EmitPluginOp = func(op *spec.Op, img *ResolvedBox) (string, bool, error) {
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
	// cluster, stays core) and the localpkg image install (its dev leg builds on
	// the host). ExternalizedBuilders is the registry fact selecting the branch.
	dg.ExternalizedBuilders = externalizedBuilders
	dg.RenderLocalPkgImageInstall = renderLocalPkgImageInstall
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
func (g *Generator) resolveDetectionBuilderStageSeam(builderName string, in spec.BuilderResolveInput, img *ResolvedBox) (spec.BuilderResolveReply, error) {
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
func (g *Generator) resolveExternalBuilderStageSeam(word, candyName string, img *ResolvedBox) (spec.BuilderResolveReply, error) {
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
func (g *Generator) resolveInlineBuilderSeam(candyName, bName string, bDef *BuilderDef, ctx *BuildStageContext, img *ResolvedBox) (string, error) {
	layer := g.Candies[candyName]
	if err := ensureBuildersConnected(context.Background(), g.Config, g.Dir, []string{bName}); err != nil {
		return "", fmt.Errorf("candy %q: connect inline builder %q: %w", candyName, bName, err)
	}
	prov, ok := providerRegistry.ResolveBuilder(bName)
	if !ok {
		return "", fmt.Errorf("candy %q: inline builder %q is externalized but its plugin is not connected", candyName, bName)
	}
	in := deploykit.BuilderResolveInputFrom(layer.Name, bName, bDef, ctx)
	reply, err := resolveBuilderStage(prov, bName, in, img)
	if err != nil {
		return "", fmt.Errorf("candy %q: inline builder %q resolve: %w", candyName, bName, err)
	}
	if strings.TrimSpace(reply.InlineFragment) == "" {
		return "", fmt.Errorf("candy %q: inline builder %q returned an empty OpResolve inline fragment", candyName, bName)
	}
	return reply.InlineFragment, nil
}

// emitTasks → deploykit.Generator.EmitTasks (P8 shim). Core resolves the render
// state via toDeploykit() (seams wired) and delegates the byte-producing emit.
func (g *Generator) emitTasks(b *strings.Builder, layer *Candy, img *ResolvedBox, ops []Op, buildDir, contextRelPrefix string) (string, error) {
	return g.toDeploykit().EmitTasks(b, layer, img, ops, buildDir, contextRelPrefix)
}

// emitPluginFragment renders a plugin verb's BUILD-context Containerfile fragment
// via the provider's OpEmit Invoke — placement-agnostic above the registry (in-proc
// for a builtin, over go-plugin gRPC for an external connected by the build-time
// plugin connect seam in NewGenerator). The plugin receives its plugin_input as
// op.Params and a spec.BuildEnv descriptor as op.Env, and returns a spec.EmitReply
// whose Fragment is spliced verbatim into the generated Containerfile. The build-time
// half of the operator-authorized build-time plugin execution.
func emitPluginFragment(prov Provider, op *Op, img *ResolvedBox) (string, error) {
	params, err := marshalJSON(op.PluginInput)
	if err != nil {
		return "", fmt.Errorf("marshal plugin_input: %w", err)
	}
	var distros []string
	if img != nil {
		distros = img.Tags
	}
	return invokeOpEmitFragment(context.Background(), prov, op.Plugin, params, distros)
}

// invokeOpEmitFragment is the ONE OpEmit → EmitReply → empty-guard → Fragment path (R3),
// shared by the build-context VERB emit (emitPluginFragment, via emitTasks) AND the
// build-context external-STEP emit (ociEmitStep, F-STEP-EMIT). It Invokes
// the provider's OpEmit with the already-marshalled params (a verb's plugin_input, or a
// step's opaque Payload) and a spec.BuildEnv descriptor, decodes the EmitReply, and returns
// the Containerfile fragment — failing LOUDLY on an empty fragment (a runtime-/deploy-only
// capability wrongly asked to build-emit; never bake nothing silently, R4). ctx MAY carry an
// in-proc reverse channel (sdk.ContextWithExecutor) so a HOST-COUPLED plugin can call back
// HostBuild during its OpEmit; a PURE plugin ignores it and returns the fragment directly.
func invokeOpEmitFragment(ctx context.Context, prov Provider, word string, params []byte, distros []string) (string, error) {
	return invokeOpEmitFragmentOpt(ctx, prov, word, params, distros, false)
}

// invokeOpEmitFragmentOpt is the OpEmit → EmitReply → Fragment core shared by the guarding
// invokeOpEmitFragment and the pod-overlay deploykit.OCITarget's compiler-emitted-step build-emit (R3).
// allowEmpty controls the empty-fragment guard: false (the default) fails LOUDLY on an empty
// fragment — a runtime-/deploy-only capability wrongly asked to build-emit; true permits an empty
// fragment, used by deploykit.OCITarget for a COMPILER-EMITTED typed step whose render is legitimately empty
// for a given instance (an empty shell snippet, a packaged service with no overrides + enable=false,
// a custom service with no unit text — exactly the cases the former the former in-core emit* returned nothing).
func invokeOpEmitFragmentOpt(ctx context.Context, prov Provider, word string, params []byte, distros []string, allowEmpty bool) (string, error) {
	env, err := marshalJSON(spec.BuildEnv{Distros: distros})
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
