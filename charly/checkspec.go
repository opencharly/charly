package main

import (
	"slices"

	"github.com/opencharly/sdk/spec"
)

// The ${NAME[:arg]} check-variable expansion grammar (ExpandTestVars / TestVarRefs /
// IsRuntimeOnlyVar / ExpandOpVars / ExpandAnyVars / CollectAnyStrings / the runtime-var
// prefixes) lives ONCE in sdk/kit (checkvars_expand.go) so a plugin candy that runs a plan
// expands ${VAR}s identically. package main references these directly as kit.X; the
// check SEMANTICS that consult spec.VerbCatalog (opEffectiveDo / opActsInBuildDeploy) stay below.

// ---------------------------------------------------------------------------
// Unified verb vocabulary — execution context, do-mode, and the VerbCatalog
// single source of truth for per-verb legality + lowering.
// ---------------------------------------------------------------------------

// ExecContext (where an op runs) and DoMode (the act/assert/instruct axis) are
// spec.ExecContext / spec.DoMode — shared vocabulary types every consumer that classifies
// an Op/Step needs (the plan walk in sdk/kit, the deploy-plan compiler in sdk/deploykit,
// this file's registry-coupled semantics, and candy/plugin-box's independent validate-engine
// re-derivation), referenced directly here (K3, #39 — no local alias). An op's Context list
// (or its VerbCatalog default) declares legality; the active engine supplies the running
// context and skips ops whose context set does not include it (VenueSkip).
//
// VerbSpec / VerbCatalog / InstallVerbs — the per-verb metadata DATA — moved to
// sdk/spec/verb_context.go (FLOOR-SLIM Unit 4): pure static data, zero registry coupling.
// The FUNCTIONS below that consult spec.VerbCatalog against the LIVE provider registry
// (providerRegistry.ResolveVerb/ResolveStep, the core-only mechanism) stay here.

// opActsInBuildDeploy reports whether the PLUGIN op c's act form has a real build/deploy install path.
// The former non-plugin (install-verb) arm was removed with the validate ENGINE (task #60): the ONLY
// caller left is the validate host-fill (fillValidateWordSets), which always asks about a `plugin:` op —
// install-verb act-capability now lives in the plugin's own installVerbSet, never here. `plugin: command`
// is act-capable via the dedicated emitCmd install path (NOT a ProvisionActor); every other plugin verb
// acts when its registered provider is a ProvisionActor / TypedStepProvider / BuildEmitter, or
// (standalone, provider not connected) when the parse-time prescan saw a plugin candy declare it.
func opActsInBuildDeploy(c *spec.Op) bool {
	if c.Plugin == "command" {
		return true
	}
	// A class:STEP plugin word (F3's external step KIND) lowers to an externalStep that ACTS at DEPLOY
	// (compileActOp resolve(ClassStep) → externalStep → OpExecute). Recognized via a connected ClassStep
	// provider OR a post-scan declaration (standalone `charly box validate`, where the step plugin is not
	// connected) — the step analogue of the verb handling below.
	if _, ok := providerRegistry.ResolveStep(c.Plugin); ok {
		return true
	}
	if isDeclaredExternalStep(c.Plugin) {
		return true
	}
	prov, ok := providerRegistry.ResolveVerb(c.Plugin)
	if !ok {
		// Not connected — the standalone `charly box validate` path, where external plugins are not
		// built+connected. Trust a verb the parse-time prescan saw a plugin candy declare
		// (registerDeclaredExternalVerb): it is build-emit-capable until the BUILD (which DOES connect it
		// via the connect seam) proves otherwise at emitPluginFragment's empty-fragment guard. A BUILTIN
		// verb always resolves above, so this branch is reached only for a genuinely external,
		// not-yet-connected verb — never for a runtime-only builtin (which is correctly rejected).
		return isDeclaredExternalVerb(c.Plugin)
	}
	// A ProvisionActor renders an install shell; a TypedStepProvider (service) lowers into a typed
	// install step; a BuildEmitter (an in-proc build-emit verb) renders a Containerfile fragment via
	// Invoke(OpEmit). Each is a real build/deploy act path — the same capability whether the plugin is
	// builtin or external (placement-agnostic).
	if _, isActor := prov.(ProvisionActor); isActor {
		return true
	}
	if _, isTyped := prov.(TypedStepProvider); isTyped {
		return true
	}
	if _, isEmitter := prov.(BuildEmitter); isEmitter {
		return true
	}
	// A CONNECTED external (out-of-process) verb is build-emit-capable via Invoke(OpEmit); the host
	// cannot type-assert capability across the process boundary, so it is trusted here and gated at build
	// by emitPluginFragment's empty-fragment guard.
	_, isExternal := prov.(*grpcProvider)
	return isExternal
}

// stampStepIntentDo writes the keyword-derived do-mode onto a verb-carrying step's Op.IntentDo (via
// the shared kit.StepDoMode derivation). It is the package-main entry the label bake (bakeableSteps)
// calls so the baked ai.opencharly.description carries intent_do deterministically — formerly a SIDE
// EFFECT of the in-core validate mutating the shared structs, which died when the validate ENGINE
// moved to candy/plugin-box (K3-D+). Kept here (checkspec.go already imports kit + owns the do-mode
// logic) so description_collect.go needs no new kit import; verb-less agent-check steps stay empty.
func stampStepIntentDo(s *spec.Step) {
	if len(s.VerbsSet()) == 0 {
		return
	}
	s.IntentDo = string(spec.StepDoMode(s))
}

// EffectiveDo returns the op's resolved do-mode: the keyword-stamped intentDo
// wins (set by the enclosing Step at run/collect time), else the verb's
// VerbCatalog default, else DoAssert.
func opEffectiveDo(c *spec.Op) spec.DoMode {
	switch spec.DoMode(c.IntentDo) {
	case spec.DoAct, spec.DoAssert, spec.DoInstruct:
		return spec.DoMode(c.IntentDo)
	}
	verb, err := c.Kind()
	if err == nil {
		if vs, ok := spec.VerbCatalog[verb]; ok && vs.DefaultDo != "" {
			return vs.DefaultDo
		}
	}
	return spec.DoAssert
}

// EffectiveContexts returns the op's resolved execution contexts: an explicit
// Context wins, else the verb's VerbCatalog default, else nil.
func opEffectiveContexts(c *spec.Op) []spec.ExecContext {
	if len(c.Context) > 0 {
		out := make([]spec.ExecContext, 0, len(c.Context))
		for _, s := range c.Context {
			out = append(out, spec.ExecContext(s))
		}
		return out
	}
	if verb, err := c.Kind(); err == nil {
		if vs, ok := spec.VerbCatalog[verb]; ok {
			return vs.Contexts
		}
	}
	return nil
}

// InContext reports whether the op is legal in ctx per its effective contexts. Its
// deploykit.OpInContext DI-hook registration lives in layers.go (which already imports
// deploykit) so this file needs no kit/deploykit import at all (K3, #39).
func opInContext(c *spec.Op, ctx spec.ExecContext) bool {
	return slices.Contains(opEffectiveContexts(c), ctx)
}
