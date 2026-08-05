package main

import (
	"context"

	"github.com/opencharly/spec/spec"
)

// validate_project_host.go — the REGISTRY-D half of `charly box validate`, and the only half left
// in the kernel.
//
// The validate ENGINE lives in the compiled-in candy/plugin-box, which fetches the error-tolerant
// resolved-project envelope from candy/plugin-build's `build:project` word (ops.OpValidate) and runs
// every rule over it. Until K-wave 2 cone R1 unit B this file ALSO served a fat
// `validate-project-checks` HostBuild seam that RE-LOADED the project host-side (LoadConfig +
// ScanAllCandyWithConfigOpts + LoadUnified + ApplyDiscover) purely so the host could run three
// validators — the CUE-schema conformance pair and the remote-candy check — over data the plugin
// already held. That was the boundary law's named RE-DERIVATION pattern: the host re-deriving
// project data the plugin owns.
//
// The justification for it ("those validators need the HOST's spliced cross-plugin CUE schema, a
// live non-marshalable cue.Value graph") was REFUTED — every CUE entry point they call is a FREE
// FUNCTION in sdk/loaderkit, which has owned its own compiled schema since cone R1 ruling 1, and
// the ProjectLoader methods the host called were bare forwards to exactly those functions. So all
// three validators folded into candy/plugin-box (validate_schema_rules.go), which already imports
// loaderkit, and charly/validate.go is DELETED.
//
// What genuinely cannot follow them is the provider REGISTRY — a kernel M-mechanism. So this file
// keeps one seam whose entire job is to answer registry questions: the plugin enumerates the words
// from its OWN envelope and sends them; the host reports which are act-capable, plus the full
// compiled-in capability set. No load, no scan, no host-side diagnostics, no project data crossing
// the wire in either direction.

// validateWordSetsBuilderKind is the F11 hostBuilders key — a generic action noun, never a provider
// word. It replaces "validate-project-checks" (itself renamed from "validate-project"), whose
// remaining non-registry work moved into the plugin.
const validateWordSetsBuilderKind = "validate-word-sets"

// hostBuildValidateWordSets answers the two REGISTRY-derived D-data word sets candy/plugin-box's
// validate rules consume as membership sets, over the inventory the plugin supplies:
//
//   - ProviderCapabilities — every compiled-in provider as "<class>:<word>". validatePluginCandy
//     checks each `source: builtin` candy's declared providers against it.
//   - ActCapableVerbs — the subset of req.PluginWords whose act form has a build/deploy install
//     path, computed by running the SAME opActsInBuildDeploy the core validator used (so builtin
//     ProvisionActor / TypedStep / BuildEmitter rejection is preserved byte-for-byte).
//
// req.ExternalProviders carries the `plugin.providers:` capability strings of the project's
// out-of-tree plugin candies, which the host REGISTERS first: a declared-but-not-yet-connected
// external verb/step is act-capable by declaration alone (opActsInBuildDeploy's
// isDeclaredExternalVerb / isDeclaredExternalStep branches), and standalone `charly box validate`
// never connects them. This is the registration the deleted registerExternalVerbsFromCandies used
// to do by re-scanning the project — the plugin reads the same three fields off its own envelope
// (CandyView.IsPlugin / PluginSource / PluginProviders) and sends them instead.
func hostBuildValidateWordSets(_ context.Context, req spec.ValidateWordSetsRequest, _ buildEngineContext) (spec.ValidateWordSetsReply, error) {
	for _, capability := range req.ExternalProviders {
		class, word, ok := splitCapability(capability)
		if !ok {
			continue
		}
		switch class {
		case ClassVerb:
			registerDeclaredExternalVerb(word)
		case ClassStep:
			registerDeclaredExternalStep(word)
		}
	}

	var reply spec.ValidateWordSetsReply
	for _, p := range providerRegistry.allProviders() {
		reply.ProviderCapabilities = append(reply.ProviderCapabilities, string(p.Class())+":"+p.Reserved())
	}

	seen := map[string]bool{}
	for _, word := range req.PluginWords {
		if word == "" || seen[word] {
			continue
		}
		seen[word] = true
		if opActsInBuildDeploy(&spec.Op{Plugin: word}) {
			reply.ActCapableVerbs = append(reply.ActCapableVerbs, word)
		}
	}
	return reply, nil
}

var _ = func() bool {
	registerHostBuilder(validateWordSetsBuilderKind, typedHostBuilder(validateWordSetsBuilderKind, hostBuildValidateWordSets))
	return true
}()
