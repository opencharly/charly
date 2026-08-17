package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/opencharly/spec/ops"
	"strings"

	"github.com/opencharly/spec/spec"
)

// The var-substitution + user-spec render helpers, the inline-content stager, and the
// per-verb Containerfile-line emitters live in sdk/deploykit (tasks_emit.go), reached by the
// candy/plugin layers and by charly's own tests (e.g. deploykit.ResolveUserSpec).
//
// The inline-builder seam (resolveInlineBuilderSeam) that used to open this file is DELETED in
// K-wave 2 cone R1: it was the last claimed host-only render seam, and the claim did not survive
// inspection — its body was the SAME OpResolve dispatch the detection and external builder legs had
// already been running plugin-side, plus a connect step that duplicated the host's own generic
// connectPluginByWordRef (now reached from the plugin as ops.InvokeProviderOpts.ExtraRef). It lives
// in sdk/deploykit's NewRenderGeneratorFromProject now, beside its two siblings.

// invokeOpEmitFragmentOpt is the ops.OpEmit → EmitReply → Fragment path for the build-context
// external-STEP emit (dispatchOCIStep, F-STEP-EMIT — the pod-overlay deploykit.OCITarget's
// compiler-emitted-step build-emit). It Invokes the provider's ops.OpEmit with the already-marshalled
// params (a step's opaque Payload) and the caller-supplied spec.BuildEnv descriptor, decodes the
// EmitReply, and returns the Containerfile fragment. (The build-context VERB emit — the
// former host-side toDeploykit()/invokeVerbBuildEmit EmitPluginOp bridge — was production-dead
// and DELETED in #55 cone-render Unit A: the live plugin-verb build-emit runs plugin-side in
// candy/plugin-build via InvokeProvider(ops.OpEmit).) ctx MAY carry an in-proc reverse channel
// (sdk.ContextWithExecutor) so a HOST-COUPLED step plugin can call back HostBuild during its
// ops.OpEmit; a PURE step plugin ignores it and returns the fragment directly.
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
	res, err := prov.Invoke(ctx, &Operation{Reserved: word, Op: ops.OpEmit, Params: params, Env: env})
	if err != nil {
		return "", err
	}
	var reply spec.EmitReply
	if res != nil && len(res.JSON) > 0 {
		if err := json.Unmarshal(res.JSON, &reply); err != nil {
			return "", fmt.Errorf("decode ops.OpEmit reply: %w", err)
		}
	}
	if !allowEmpty && strings.TrimSpace(reply.Fragment) == "" {
		return "", fmt.Errorf("plugin %q returned an empty ops.OpEmit fragment — it has no build-context act (a runtime-only verb in a build run: step, or a deploy-only step declaring emits without an ops.OpEmit fragment? use context: [runtime] / set emits=false)", word)
	}
	return reply.Fragment, nil
}
