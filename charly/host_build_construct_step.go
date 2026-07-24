package main

import (
	"context"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// host_build_construct_step.go — the "construct-step" F10 host-builder (K5-A item 1,
// compile-seam ctx-threading): the ONE genuinely host-only piece of the former
// charly/install_build_act.go:compileActOp — resolving a `run: plugin: <word>` op's
// word against the PROVIDER REGISTRY (a clause-M kernel mechanism a plugin cannot hold
// its own copy of) to decide how it lowers. Everything ELSE compileActOp did (the
// generic OpStep construction for every non-plugin verb) is now fully portable and
// lives in sdk/deploykit's buildGenericOpStep — this handler is reached ONLY for the
// `verb == "plugin"` sub-case, via deploykit.constructOpStep.
//
// An EMPTY reply (Step == nil, no error) means "not special — build the generic
// OpStep", exactly as compileActOp's own fallthrough did; the caller (deploykit)
// performs that construction itself rather than paying a second round-trip for it.
const constructStepBuilderKind = "construct-step"

func hostBuildConstructStep(_ context.Context, req spec.ConstructStepRequest, _ buildEngineContext) (spec.ConstructStepReply, error) {
	op := &req.Op
	verb, err := op.Kind()
	if err != nil || verb != "plugin" {
		return spec.ConstructStepReply{}, nil
	}

	// VERB-FIRST PRECEDENCE — see compileActOp's original doc comment (install_build_act.go,
	// git history): a `run: plugin: <word>` whose word resolves as a VERB is a verb act,
	// resolved BEFORE the class:step authored-external-step branch below (a class:step WORD
	// can collide with a verb word, e.g. `file`).
	if prov, ok := providerRegistry.ResolveVerb(op.Plugin); ok {
		if stepprov, ok := prov.(TypedStepProvider); ok {
			step := stepprov.ConstructStep(op, stepConstructCtx{
				RunAsUser:  req.ResolvedUser,
				CandyName:  req.CandyName,
				PkgFormat:  req.PkgFormat,
				DistroTags: req.DistroTags,
			})
			return constructStepReplyFor(step), nil
		}
		// An EXTERNAL (out-of-process) plugin verb has no in-proc ProvisionActor shell — it
		// EXECUTES its deploy-context effect at deploy over the reverse channel (OpExecute)
		// and bakes its build-context fragment via OpEmit. Route it to ExternalPluginStep.
		if _, ok := prov.(executorInvoker); ok {
			return constructStepReplyFor(&deploykit.ExternalPluginStep{
				Op:           op,
				CandyName:    req.CandyName,
				ResolvedUser: req.ResolvedUser,
				Distros:      req.DistroTags,
			}), nil
		}
		// An in-proc verb (a ProvisionActor like `file`, or `command`) that is neither a
		// TypedStepProvider nor an out-of-process executorInvoker → the generic OpStep
		// (deliberately NOT the class:step branch below — verb-first).
		return spec.ConstructStepReply{}, nil
	}
	if sp, ok := providerRegistry.resolve(ClassStep, op.Plugin); ok {
		// The word is NOT a verb → an authored external step KIND: a class:step provider
		// DECLARING a StepContract (F3). The opaque Payload is the op's plugin_input, and
		// Scope/Venue/Gate come from the plugin's declared contract.
		if carrier, ok := sp.(spec.StepContractCarrier); ok {
			if sc, ok := carrier.DeclaredStepContract(); ok {
				payload, _ := marshalJSON(op.PluginInput)
				return constructStepReplyFor(&deploykit.ExternalStep{
					Word:      op.Plugin,
					ScopeV:    sc.Scope,
					VenueV:    sc.Venue,
					GateV:     sc.Gate,
					Payload:   payload,
					CandyName: req.CandyName,
				}), nil
			}
		}
	}
	return spec.ConstructStepReply{}, nil
}

// constructStepReplyFor projects a constructed InstallStep to its wire view — the SAME
// StepToView/StepFromView round-trip every other step-carrying seam uses (R3).
func constructStepReplyFor(step spec.InstallStep) spec.ConstructStepReply {
	if step == nil {
		return spec.ConstructStepReply{}
	}
	view := deploykit.StepToView(step)
	return spec.ConstructStepReply{Step: &view}
}

var _ = func() bool {
	registerHostBuilder(constructStepBuilderKind, typedHostBuilder(constructStepBuilderKind, hostBuildConstructStep))
	return true
}()
