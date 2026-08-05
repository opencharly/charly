package main

import (
	"github.com/opencharly/spec/spec"
)

// opActsInBuildDeploy reports whether the plugin op c's act form has a real build/deploy install
// path — the act-capability question validate_project_host.go's word-sets leg answers for the
// validate plugin's envelope-derived word inventory. `plugin: command` acts via the emitCmd
// install path; every other plugin verb acts when its registered provider is a ProvisionActor /
// TypedStepProvider / BuildEmitter, or (standalone) when the parse-time prescan declared it.
func opActsInBuildDeploy(c *spec.Op) bool {
	if c.Plugin == "command" {
		return true
	}
	// A class:STEP plugin word (F3's external step KIND) lowers to an externalStep that ACTS at DEPLOY
	// (hostBuildConstructStep resolve(ClassStep) → externalStep → ops.OpExecute), recognized via a
	// connected ClassStep provider OR a post-scan declaration — the step analogue of the verb path.
	if _, ok := providerRegistry.ResolveStep(c.Plugin); ok {
		return true
	}
	if isDeclaredExternalStep(c.Plugin) {
		return true
	}
	prov, ok := providerRegistry.ResolveVerb(c.Plugin)
	if !ok {
		// Not connected — the standalone `charly box validate` path. Trust a verb the parse-time
		// prescan declared (registerDeclaredExternalVerb): build-emit-capable until the BUILD connects
		// it and the plugin-verb ops.OpEmit empty-fragment guard proves otherwise. A BUILTIN verb
		// always resolves above, so this branch is reached only for a genuinely external verb.
		return isDeclaredExternalVerb(c.Plugin)
	}
	// A ProvisionActor renders an install shell; a TypedStepProvider (service) lowers into a typed
	// install step; a BuildEmitter renders a Containerfile fragment via Invoke(ops.OpEmit).
	if _, isActor := prov.(ProvisionActor); isActor {
		return true
	}
	if _, isTyped := prov.(TypedStepProvider); isTyped {
		return true
	}
	if _, isEmitter := prov.(BuildEmitter); isEmitter {
		return true
	}
	// A CONNECTED external (out-of-process) verb is build-emit-capable via Invoke(ops.OpEmit); the
	// host cannot type-assert capability across the process boundary, so it is trusted here and gated
	// at build by the plugin-verb ops.OpEmit empty-fragment guard.
	_, isExternal := prov.(*grpcProvider)
	return isExternal
}
