package installstep

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	pb "github.com/opencharly/spec/proto"
	"github.com/opencharly/spec/spec"
)

// oci_dispatch.go — the FULL core pod-overlay step-emit dispatch, RELOCATED here from charly's
// former host-only registry-consult + StepContract gating + Invoke logic (K5-A item 2). The host's
// deploykit.OCITarget walker (sdk/deploykit) still reaches this via HostBuild("step-emit",
// {Word:"oci-emit-step", …}); charly/step_emit_hostbuild.go's stepEmitOCIEmitStep is now a THIN
// forwarder that decodes the cached overlay buildEngineContext into a spec.BuildEnv and Invokes
// THIS plugin's "oci-dispatch" word (in-proc, compiled-in) with the step/plan wire views —
// candy/plugin-installstep is already the seam's OTHER 12 words' server, so it is the natural home
// for the dispatch DECISION too (R3: one relocation target, not a second seam).
//
// The dispatch itself is byte-identical to the former host-only logic: an authored external step
// ("external:<word>") or one of the compiler-emitted kinds resolves through the class:step
// registry — but instead of a direct in-core providerRegistry.resolve + StepContractCarrier
// consult, it goes through the generic reverse-channel DescribeProvider (cached capability
// metadata, no live Invoke — the K5-A item 2 RPC this plugin was built to prove) + InvokeProvider
// (dispatch) legs. The ONE remaining non-class:step kind (StepKindExternalPlugin — a `run: plugin:
// <verb>` step) dispatches to its class:verb provider via InvokeProvider directly, mirroring the
// former host-only invokeVerbBuildEmit/externalPluginStepProvider.EmitOCI contract (no Emits gate —
// a deploy-only plugin's empty OpEmit fragment is a loud failure, never a silent skip, R4).

// emitOCIDispatch decodes the relocated spec.OCIEmitStepParams{StepView, PlanView} payload +
// the caller's spec.BuildEnv (Distros/Image/DevLocalPkg/ImageBuildDir/ContextRelPrefix, forwarded
// from the host's cached overlay buildEngineContext), reconstructs the step, and dispatches to
// whichever peer provider serves its pod-overlay Containerfile fragment.
func emitOCIDispatch(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	var p spec.OCIEmitStepParams
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &p); err != nil {
			return nil, fmt.Errorf("plugin-installstep: decode oci-dispatch params: %w", err)
		}
	}
	var env spec.BuildEnv
	if len(req.GetEnvJson()) > 0 {
		if err := json.Unmarshal(req.GetEnvJson(), &env); err != nil {
			return nil, fmt.Errorf("plugin-installstep: decode oci-dispatch build env: %w", err)
		}
	}
	step, err := deploykit.StepFromView(p.StepView)
	if err != nil {
		return nil, fmt.Errorf("oci-dispatch: reconstruct step: %w", err)
	}

	exec, err := sdk.ExecutorForInvoke(ctx, req.GetExecutorBrokerId())
	if err != nil {
		return nil, fmt.Errorf("plugin-installstep: reach host reverse channel for oci-dispatch: %w", err)
	}

	var frag string
	switch {
	case deploykit.IsExternalStepKind(step.Kind()):
		s := step.(*spec.ExternalStep)
		frag, err = dispatchClassStep(ctx, exec, s.Word, s.Payload, env, false)
	case deploykit.PluginEmitStepWords[step.Kind()] != "":
		word := deploykit.PluginEmitStepWords[step.Kind()]
		payload, merr := json.Marshal(p.StepView)
		if merr != nil {
			return nil, fmt.Errorf("oci-dispatch: marshal %s step view: %w", step.Kind(), merr)
		}
		frag, err = dispatchClassStep(ctx, exec, word, payload, env, true)
	case step.Kind() == deploykit.StepKindExternalPlugin:
		s := step.(*spec.ExternalPluginStep)
		frag, err = dispatchExternalPluginVerb(ctx, exec, s, env)
	default:
		return nil, fmt.Errorf("oci-dispatch: unknown step kind %q", step.Kind())
	}
	if err != nil {
		return nil, err
	}
	if frag != "" && !strings.HasSuffix(frag, "\n") {
		frag += "\n"
	}
	return replyFragment(frag)
}

// dispatchClassStep resolves the class:step provider serving `word` via the CACHED-metadata
// DescribeProvider RPC (no live Invoke) and, when its declared StepContract.Emits is true,
// dispatches OpEmit via InvokeProvider — the plugin-side mirror of the former host-only
// registry-consult (providerRegistry.resolve + StepContractCarrier consult + prov.Invoke).
// allowEmpty=false (an authored external step MUST produce a fragment); allowEmpty=true tolerates a
// legitimately-empty render for a compiler-emitted kind (an empty snippet, a no-override packaged
// service, …).
func dispatchClassStep(ctx context.Context, exec *sdk.Executor, word string, payload []byte, env spec.BuildEnv, allowEmpty bool) (string, error) {
	found, contract, err := exec.DescribeProvider(ctx, "step", word)
	if err != nil {
		return "", fmt.Errorf("oci-dispatch: describe class:step %q: %w", word, err)
	}
	if !found {
		return "", fmt.Errorf("oci-dispatch: class:step provider %q not connected at build time", word)
	}
	if contract == nil || !contract.Emits {
		// A deploy-only step (like apk on an image build): recorded, not baked.
		return "", nil
	}
	return invokeOpEmitFragment(ctx, exec, "step", word, payload, env, allowEmpty)
}

// dispatchExternalPluginVerb serves the ONE remaining non-class:step kind (a `run: plugin: <verb>`
// step): dispatch OpEmit to the class:verb provider via InvokeProvider directly — no Emits gate,
// matching the former host-only invokeVerbBuildEmit/externalPluginStepProvider.EmitOCI contract (a
// deploy-only plugin's empty fragment is a loud failure, never a silent skip, R4).
func dispatchExternalPluginVerb(ctx context.Context, exec *sdk.Executor, s *spec.ExternalPluginStep, env spec.BuildEnv) (string, error) {
	if s.Op == nil || s.Op.Plugin == "" {
		return "", fmt.Errorf("oci-dispatch: external plugin step carries no plugin verb")
	}
	params, err := json.Marshal(s.Op.PluginInput)
	if err != nil {
		return "", fmt.Errorf("oci-dispatch: marshal plugin_input: %w", err)
	}
	return invokeOpEmitFragment(ctx, exec, "verb", s.Op.Plugin, params, env, false)
}

// invokeOpEmitFragment is the ONE OpEmit → EmitReply → Fragment core shared by both dispatch arms
// above (R3) — the plugin-side mirror of charly's own invokeOpEmitFragmentOpt (tasks.go), now
// reached via the generic InvokeProvider peer-dispatch RPC instead of a direct in-core prov.Invoke.
func invokeOpEmitFragment(ctx context.Context, exec *sdk.Executor, class, word string, params []byte, env spec.BuildEnv, allowEmpty bool) (string, error) {
	envJSON, err := json.Marshal(env)
	if err != nil {
		return "", fmt.Errorf("oci-dispatch: marshal build env: %w", err)
	}
	resJSON, err := exec.InvokeProvider(ctx, class, word, opEmit, params, envJSON, sdk.InvokeProviderOpts{})
	if err != nil {
		return "", fmt.Errorf("oci-dispatch: %s:%s build-emit: %w", class, word, err)
	}
	var reply spec.EmitReply
	if len(resJSON) > 0 {
		if err := json.Unmarshal(resJSON, &reply); err != nil {
			return "", fmt.Errorf("oci-dispatch: decode %s:%s OpEmit reply: %w", class, word, err)
		}
	}
	if !allowEmpty && strings.TrimSpace(reply.Fragment) == "" {
		return "", fmt.Errorf("oci-dispatch: %s:%s returned an empty OpEmit fragment", class, word)
	}
	return reply.Fragment, nil
}
