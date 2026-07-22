package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
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
	InvokeWithExecutor(ctx context.Context, op *Operation, exec deploykit.DeployExecutor, build buildEngineContext, rebootable bool, cc *checkContextReverseServer) (*Result, error)
}

// externalPluginStepProvider is the StepKindExternalPlugin StepProvider. Each Emit*
// picks the right Invoke op for its venue, placement-agnostic above the registry.
type externalPluginStepProvider struct{ builtinStepBase }

func (externalPluginStepProvider) Reserved() string { return string(spec.StepKindExternalPlugin) }

// EmitOCI is the BUILD venue (image build / pod-overlay Containerfile): an external
// plugin verb bakes its build-context output via Invoke(OpEmit) through the SHARED
// emitPluginFragment seam (R3) — the SAME path the box-build emitTasks `case "plugin"`
// takes for an external verb. It CANNOT deploy-execute at build (no live venue); a
// deploy-only plugin (empty OpEmit fragment) fails loudly at emitPluginFragment's
// empty-fragment guard, never bakes nothing silently. Returns the fragment (P11c: the
// StepProvider.EmitOCI signature returns the string the caller splices, decoupling it
// from the the walker buffer that now lives in sdk/deploykit); build.Box supplies the
// image the plugin verb's distros are read from.
func (externalPluginStepProvider) EmitOCI(step spec.InstallStep, _ *deploykit.InstallPlan, build buildEngineContext) (string, error) {
	s := step.(*deploykit.ExternalPluginStep)
	prov, ok := providerRegistry.ResolveVerb(s.Op.Plugin)
	if !ok {
		return "", fmt.Errorf("oci-emit-step: external plugin verb %q is not connected at build time", s.Op.Plugin)
	}
	frag, err := emitPluginFragment(prov, s.Op, build.Box)
	if err != nil {
		return "", fmt.Errorf("external plugin verb %q build-emit: %w", s.Op.Plugin, err)
	}
	if !strings.HasSuffix(frag, "\n") {
		frag += "\n"
	}
	return frag, nil
}

// The guest/host DEPLOY venue is no longer an in-proc Emit* method: BOTH target:local AND
// target:vm externalized into candy/plugin-deploy-local / candy/plugin-deploy-vm, whose
// kit.WalkPlans routes an ExternalPluginStep (or the F3 ExternalStep) through the host's
// RunHostStep reverse leg, which dispatches OpExecute via the SAME PLUGIN↔PLUGIN
// InvokeProvider leg (plugin_dispatch_reverse.go) every other peer-invoke uses — RunHostStep's
// invokeExternalStep helper (plugin_executor_reverse.go) calls it directly, in-process (no wire
// hop, since RunHostStep already runs on the executorReverseServer InvokeProvider is served
// from). EmitOCI (the pod-overlay build venue) is the only remaining in-proc Emit* this
// provider implements.

// Self-register at package-var init (before any init(), so the per-class step bijection
// gate in registry_bootstrap.go observes it without a cross-init race).
var _ = registerDedicatedBuiltin(externalPluginStepProvider{})
