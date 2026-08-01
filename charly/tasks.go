package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/opencharly/spec/spec"
)

// The var-substitution + user-spec render helpers, the inline-content stager, and the
// per-verb Containerfile-line emitters live in sdk/deploykit (tasks_emit.go), reached by the
// candy/plugin layers and by charly's own tests (e.g. deploykit.ResolveUserSpec). charly CORE
// no longer imports deploykit: this file's inline-builder seam builds its spec.BuilderResolveInput
// via spec.BuilderResolveInputFrom — relocated to spec/spec (buildwire_render.go, #55 coneK3tasks),
// the cone-render precedent (a render primitive in spec/spec so the host shares ONE source with
// the plugins and needs no kit import). The builder-emit cluster (ensureBuildersConnected +
// registry ResolveBuilder + resolveBuilderStage) is registry-coupled and stays core.

// resolveInlineBuilderSeam is the core impl wired onto deploykit's
// ResolveInlineBuilder seam: connect + OpResolve an externalized INLINE builder,
// returning its C10 InlineFragment (or a per-failure error, byte-preserved). The
// builder-emit cluster (ensureBuildersConnected + registry ResolveBuilder +
// resolveBuilderStage) is registry-coupled and stays core.
func (g *Generator) resolveInlineBuilderSeam(candyName, bName string, bDef *spec.BuilderDef, ctx *spec.BuildStageContext, img *spec.ResolvedBox) (string, error) {
	layer := g.Candies[candyName]
	if err := ensureBuildersConnected(context.Background(), g.Config, g.Dir, []string{bName}); err != nil {
		return "", fmt.Errorf("candy %q: connect inline builder %q: %w", candyName, bName, err)
	}
	prov, ok := providerRegistry.ResolveBuilder(bName)
	if !ok {
		return "", fmt.Errorf("candy %q: inline builder %q is externalized but its plugin is not connected", candyName, bName)
	}
	in := spec.BuilderResolveInputFrom(layer.GetName(), bName, bDef, ctx)
	reply, err := resolveBuilderStage(prov, bName, in, img)
	if err != nil {
		return "", fmt.Errorf("candy %q: inline builder %q resolve: %w", candyName, bName, err)
	}
	if strings.TrimSpace(reply.InlineFragment) == "" {
		return "", fmt.Errorf("candy %q: inline builder %q returned an empty OpResolve inline fragment", candyName, bName)
	}
	return reply.InlineFragment, nil
}

// invokeOpEmitFragmentOpt is the OpEmit → EmitReply → Fragment path for the build-context
// external-STEP emit (ociEmitStep, F-STEP-EMIT — the pod-overlay deploykit.OCITarget's
// compiler-emitted-step build-emit). It Invokes the provider's OpEmit with the already-marshalled
// params (a step's opaque Payload) and the caller-supplied spec.BuildEnv descriptor, decodes the
// EmitReply, and returns the Containerfile fragment. (The build-context VERB emit — the
// former host-side toDeploykit()/invokeVerbBuildEmit EmitPluginOp bridge — was production-dead
// and DELETED in #55 cone-render Unit A: the live plugin-verb build-emit runs plugin-side in
// candy/plugin-build via InvokeProvider(OpEmit).) ctx MAY carry an in-proc reverse channel
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
