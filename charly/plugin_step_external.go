package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/opencharly/charly/charly/spec"
)

// plugin_step_external.go — the StepProvider for StepKindExternalPlugin: the
// install-timeline IR node for a `run: plugin: <verb>` step served by an
// OUT-OF-PROCESS plugin. It is the DEPLOY-context (Local/VM) + pod-overlay (OCI) leg
// of the operator-authorized plugin execution, the counterpart of the build-context
// OpEmit leg in tasks.go (emitPluginFragment). Self-registers via
// registerDedicatedBuiltin, like every other dedicated step provider.

// executorInvoker is the capability to Invoke a deploy/step/builder op WITH the E3b
// reverse channel: the provider stands up the host's ExecutorService on the go-plugin
// broker and the out-of-process plugin dials back to run shell/SSH ops on the live
// venue. Only *grpcProvider (the broker-carrying out-of-proc peer) implements it — a
// built-in verb runs in-proc and has no out-of-proc execute — so it is the precise
// discriminator that routes an EXTERNAL plugin verb to ExternalPluginStep while every
// builtin (command + the ProvisionActor verbs) stays on the OpStep path. Mirrors the
// build-context BuildEmitter marker interface (provider_verb.go).
type executorInvoker interface {
	InvokeWithExecutor(ctx context.Context, op *Operation, exec DeployExecutor, build buildEngineContext, rebootable bool, cc *checkContextReverseServer) (*Result, error)
}

// externalPluginStepProvider is the StepKindExternalPlugin StepProvider. Each Emit*
// picks the right Invoke op for its venue, placement-agnostic above the registry.
type externalPluginStepProvider struct{ builtinStepBase }

func (externalPluginStepProvider) Reserved() string { return string(StepKindExternalPlugin) }

// EmitOCI is the BUILD venue (image build / pod-overlay Containerfile): an external
// plugin verb bakes its build-context output via Invoke(OpEmit) through the SHARED
// emitPluginFragment seam (R3) — the SAME path the box-build emitTasks `case "plugin"`
// takes for an external verb. It CANNOT deploy-execute at build (no live venue); a
// deploy-only plugin (empty OpEmit fragment) fails loudly at emitPluginFragment's
// empty-fragment guard, never bakes nothing silently.
func (externalPluginStepProvider) EmitOCI(t *OCITarget, step InstallStep, _ *InstallPlan) error {
	s := step.(*ExternalPluginStep)
	prov, ok := providerRegistry.ResolveVerb(s.Op.Plugin)
	if !ok {
		return fmt.Errorf("OCITarget: external plugin verb %q is not connected at build time", s.Op.Plugin)
	}
	frag, err := emitPluginFragment(prov, s.Op, t.Box)
	if err != nil {
		return fmt.Errorf("external plugin verb %q build-emit: %w", s.Op.Plugin, err)
	}
	t.buf.WriteString(frag)
	if !strings.HasSuffix(frag, "\n") {
		t.buf.WriteString("\n")
	}
	return nil
}

// The guest/host DEPLOY venue is no longer an in-proc Emit* method: BOTH target:local AND
// target:vm externalized into candy/plugin-deploy-local / candy/plugin-deploy-vm, whose
// kit.WalkPlans routes an ExternalPluginStep through the host's RunHostStep reverse leg —
// the executeExternalPluginStep seam below (R3). EmitOCI (the pod-overlay build venue) is
// the only remaining in-proc Emit* this provider implements.

// executeExternalPluginStep Invokes the external plugin verb's OpExecute over the E3b
// reverse channel and returns the decoded DeployReply (its ReverseOps recorded by the
// caller). plugin_input rides op.Params UNWRAPPED (the SAME shape emitPluginFragment /
// the externalDeployTarget marshal — R3), a spec.DeployVenue rides op.Env, and the
// live executor is stood up on the broker by InvokeWithExecutor so the plugin runs its
// effect on the real venue. The reverse channel is NOT rebootable here (a nested verb-step
// never reboots the venue; only a RebootStep on a vm deploy does). Reached from the host's
// RunHostStep when a deploy plugin walks an ExternalPluginStep (the nested reverse channel).
func executeExternalPluginStep(ctx context.Context, s *ExternalPluginStep, plan *InstallPlan, exec DeployExecutor, build buildEngineContext) (spec.DeployReply, error) {
	var zero spec.DeployReply
	prov, ok := providerRegistry.ResolveVerb(s.Op.Plugin)
	if !ok {
		return zero, fmt.Errorf("external plugin step %q: verb is not connected at deploy time", s.Op.Plugin)
	}
	inv, ok := prov.(executorInvoker)
	if !ok {
		// A non-external provider reached here — compileActOp only routes external
		// grpcProviders to ExternalPluginStep, so this is an internal invariant breach,
		// never an authoring error (those are caught at compileActOp / validate).
		return zero, fmt.Errorf("external plugin step %q: verb has no deploy-context execute (not an out-of-process plugin)", s.Op.Plugin)
	}
	params, err := marshalJSON(s.Op.PluginInput)
	if err != nil {
		return zero, fmt.Errorf("external plugin step %q: marshal plugin_input: %w", s.Op.Plugin, err)
	}
	return invokeStepExecute(ctx, s.Op.Plugin, inv, params, externalStepVenueName(plan), exec, build)
}

// executeExternalStep dispatches an EXTERNAL (plugin-contributed) step kind (F3) to its
// serving class:step provider's OpExecute over the reverse channel. Unlike
// executeExternalPluginStep (a ClassVerb wrapped in the fixed ExternalPlugin kind, params =
// plugin_input), the provider is resolved by ClassStep and the OpExecute params are the step's
// OPAQUE Payload verbatim. Shares invokeStepExecute (R3); the reply's ReverseOps are recorded
// by the caller (dynamic teardown).
func executeExternalStep(ctx context.Context, s *externalStep, plan *InstallPlan, exec DeployExecutor, build buildEngineContext) (spec.DeployReply, error) {
	var zero spec.DeployReply
	prov, ok := providerRegistry.resolve(ClassStep, s.Word)
	if !ok {
		return zero, fmt.Errorf("external step %q: class:step provider not connected at deploy time", s.Word)
	}
	inv, ok := prov.(executorInvoker)
	if !ok {
		return zero, fmt.Errorf("external step %q: provider has no deploy-context execute (not an out-of-process plugin)", s.Word)
	}
	return invokeStepExecute(ctx, s.Word, inv, s.Payload, externalStepVenueName(plan), exec, build)
}

// invokeStepExecute is the shared OpExecute dispatch for an external step (R3): it stands up
// the live executor on the provider's go-plugin broker (InvokeWithExecutor), Invokes OpExecute
// with the opaque params + a venue descriptor, and decodes the DeployReply (its ReverseOps the
// caller records). Used by executeExternalPluginStep (verb-step) AND executeExternalStep
// (plugin-contributed step kind).
func invokeStepExecute(ctx context.Context, word string, inv executorInvoker, params []byte, venueName string, exec DeployExecutor, build buildEngineContext) (spec.DeployReply, error) {
	var zero spec.DeployReply
	env, err := marshalJSON(spec.DeployVenue{DeployName: venueName})
	if err != nil {
		return zero, fmt.Errorf("external step %q: marshal venue: %w", word, err)
	}
	res, err := inv.InvokeWithExecutor(ctx,
		&Operation{Reserved: word, Op: OpExecute, Params: params, Env: env}, exec, build, false, nil)
	if err != nil {
		return zero, err
	}
	var reply spec.DeployReply
	if res != nil && len(res.JSON) > 0 {
		if err := json.Unmarshal(res.JSON, &reply); err != nil {
			return zero, fmt.Errorf("external step %q: decode reply: %w", word, err)
		}
	}
	return reply, nil
}

// externalStepVenueName derives the venue descriptor's DeployName from the plan's
// identity (box name, else single-candy name, else the deploy-id hash) — the analogue
// of the externalDeployTarget passing its deploy name, so a plugin can derive a
// deterministic per-deploy scratch location.
func externalStepVenueName(plan *InstallPlan) string {
	switch {
	case plan == nil:
		return ""
	case plan.Box != "":
		return plan.Box
	case plan.Candy != "":
		return plan.Candy
	default:
		return plan.DeployID
	}
}

// Self-register at package-var init (before any init(), so the per-class step bijection
// gate in registry_bootstrap.go observes it without a cross-init race).
var _ = registerDedicatedBuiltin(externalPluginStepProvider{})
