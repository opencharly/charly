package main

import (
	"maps"

	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"

	"github.com/opencharly/sdk/deploykit"
)

func init() { deploykit.CompileActOp = compileActOp }

func compileActOp(op *spec.Op, layer deploykit.CandyModel, img *buildkit.ResolvedBox) spec.InstallStep {
	verb, err := op.Kind()
	if err != nil {
		return nil
	}
	userDir, _ := resolveUserSpec(op.RunAs, img)
	// A `plugin:` verb whose provider lowers into a TYPED install step (package →
	// SystemPackagesStep, service → ServicePackagedStep) constructs that step here —
	// BEFORE the generic OpStep fallthrough — so its Reverse() records the load-bearing
	// reversals. The typed step then flows through the SAME Emit{OCI,Local,VM} + Reverse()
	// as before the verb was extracted. A `plugin:` verb whose provider is NOT a
	// TypedStepProvider (command, the RenderProvisionScript verbs) falls through to OpStep,
	// unchanged.
	if verb == "plugin" {
		// VERB-FIRST PRECEDENCE. A `run: plugin: <word>` whose word resolves as a VERB is a
		// verb act — resolved here BEFORE the class:step authored-external-step branch below.
		// This matters because a class:step WORD can COLLIDE with a verb word: `file` is both
		// `verb:file` (an in-proc ProvisionActor that drops a file) AND `step:file` (the C1.1
		// build-emit-only class:step plugin candy/plugin-installstep). The author's
		// `run: plugin: file` means the VERB; it must NOT be hijacked into an `external:file`
		// step (which the deploy walk would route to OpExecute — a leg the build-emit-only
		// plugin cannot serve). So the class:step branch is reached ONLY when the word is NOT a
		// verb (an authored external step KIND like examplestepkind).
		if prov, ok := providerRegistry.ResolveVerb(op.Plugin); ok {
			if stepprov, ok := prov.(TypedStepProvider); ok {
				return stepprov.ConstructStep(op, layer, img)
			}
			// An EXTERNAL (out-of-process) plugin verb has no in-proc ProvisionActor
			// shell — it EXECUTES its deploy-context effect at deploy over the E3b
			// reverse channel (Invoke(OpExecute) WITH the live executor), and bakes its
			// build-context fragment via Invoke(OpEmit). Route it to ExternalPluginStep.
			// The discriminator is the executorInvoker capability, which only the
			// grpcProvider (broker-carrying out-of-proc peer) satisfies — so `command`
			// and every built-in ProvisionActor verb fall through to the OpStep path
			// below (renderOpCommand), unchanged. The build-context counterpart
			// (emitTasks `case "plugin"`) stays the box-build seam; this is the
			// DEPLOY-context (Local/VM) + pod-overlay (OCI) leg.
			if _, ok := prov.(executorInvoker); ok {
				return &deploykit.ExternalPluginStep{
					Op:           op,
					CandyName:    layer.GetName(),
					ResolvedUser: userDir,
					Distros:      img.Tags,
				}
			}
			// An in-proc verb (a ProvisionActor like `file`, or `command`) that is neither a
			// TypedStepProvider nor an out-of-process executorInvoker → the generic OpStep below
			// (its deploy act renders via resolveProvisionScript; its build-emit via emitTasks
			// `case "plugin"`). Deliberately NOT the class:step branch (verb-first, above).
		} else if sp, ok := providerRegistry.resolve(ClassStep, op.Plugin); ok {
			// The word is NOT a verb → an authored external step KIND: a class:step provider
			// DECLARING a StepContract (F3, e.g. examplestepkind). The opaque Payload is the op's
			// plugin_input, and Scope/Venue/Gate come from the plugin's declared contract. The
			// host walks it via the open default arm + dispatches OpExecute to the serving plugin
			// (executeExternalStep). The C1.1 build-emit-only class:step words never reach here —
			// `file` is a verb (handled above), and the other six (shell-hook/shell-snippet/
			// service-packaged/service-custom/repo-change/apk-install) are compiler-emitted NATIVE
			// step kinds, never authored as a `run: plugin:` op.
			if carrier, ok := sp.(spec.StepContractCarrier); ok {
				if sc, ok := carrier.DeclaredStepContract(); ok {
					payload, _ := marshalJSON(op.PluginInput)
					return &deploykit.ExternalStep{
						Word:      op.Plugin,
						ScopeV:    sc.Scope,
						VenueV:    sc.Venue,
						GateV:     sc.Gate,
						Payload:   payload,
						CandyName: layer.GetName(),
					}
				}
			}
		}
	}
	// Install verbs (mkdir/copy/write/link/download/setcap/build) + command →
	// a generic OpStep (existing emit + Reverse). Snapshot layer.Vars() so the
	// host/local renderer can emit `export K=V` (build-time gets these via
	// Containerfile ENV). Tokenize a home-relative `to:` so each DeployTarget
	// resolves it against the real destination home at emit.
	var candyVars map[string]string
	if len(layer.Vars()) > 0 {
		candyVars = make(map[string]string, len(layer.Vars()))
		maps.Copy(candyVars, layer.Vars())
	}
	var resolvedTo string
	if op.To != "" {
		resolvedTo = kit.ExpandPath(op.To, deploykit.HomeToken)
	}
	return &deploykit.OpStep{
		Op:           op,
		CandyName:    layer.GetName(),
		CandyDir:     layer.GetSourceDir(),
		CtxPath:      layer.GetSourceDir(),
		ResolvedUser: userDir,
		CandyVars:    candyVars,
		To:           resolvedTo,
		Distros:      img.Tags,
	}
}
