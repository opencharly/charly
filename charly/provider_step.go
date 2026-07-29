package main

import (
	"fmt"

	"github.com/opencharly/spec/spec"
)

// provider_step.go — the pod-overlay build-emit BIJECTION gate. K5-A item 2 relocated the FULL
// per-kind dispatch DECISION (which peer renders a given InstallStep's Containerfile fragment) out
// of core entirely, into candy/plugin-installstep's "oci-dispatch" word — including the ONE
// remaining kind that used to have an in-proc StepProvider (ExternalPlugin, formerly
// externalPluginStepProvider.EmitOCI in charly/plugin_step_external.go). That file, the StepProvider
// interface, builtinStepBase, and stepProviderFor are DELETED (R1/R2: EmitOCI's relocation made them
// genuinely dead — zero remaining callers — not merely "no longer the primary path"; the F11
// zero-per-kind-residue self-test caught this). ExternalPlugin's build-emit is now an
// UNCONDITIONAL Go-level type-switch arm in candy/plugin-installstep/oci_dispatch.go
// (dispatchExternalPluginVerb), needing no registry entry at all — the plugin resolves ITS
// class:verb target directly via InvokeProvider, bypassing the (now-deleted) ClassStep
// "ExternalPlugin" registration entirely.

// pluginEmitStepWords is the host-side alias for spec.PluginEmitStepWords (K5-A item 2): the
// map RELOCATED to sdk/deploykit so the plugin-side "oci-dispatch" word (candy/plugin-installstep)
// can consult the SAME kind→word vocabulary the host's checkStepProviderBijection uses — R3, one
// source of truth for the 12 compiler-emitted kinds' build-emit word mapping. See
// spec.PluginEmitStepWords for the full doc (categories, host-coupled vs pure).
var pluginEmitStepWords = spec.PluginEmitStepWords

// checkStepProviderBijection asserts every InstallStep kind is SERVED. The 12 kinds in
// pluginEmitStepWords must resolve to a compiled-in class:step plugin declaring a StepContract
// (its build-emit); the ONE remaining kind (ExternalPlugin) needs NO registry entry at all — its
// build-emit is an unconditional Go-level type-switch arm in candy/plugin-installstep's
// "oci-dispatch" word, verified by compilation (spec.AllStepKinds is a closed, compile-time
// enumeration) rather than a runtime registry lookup. Run in the same init() that registers, after
// registration (the compiled-in plugins register first — plugins_generated.go's init precedes
// registry_bootstrap.go's alphabetically, the SAME ordering checkVerbProviderBijection relies on).
func checkStepProviderBijection() error {
	var missing []string
	for _, k := range spec.AllStepKinds {
		if k == spec.StepKindExternalPlugin {
			// Handled unconditionally by candy/plugin-installstep's oci-dispatch Go-level switch;
			// no ClassStep registry entry exists (or is needed) for this kind anymore.
			continue
		}
		word, isPlugin := pluginEmitStepWords[k]
		if !isPlugin {
			missing = append(missing, fmt.Sprintf("%s (not in pluginEmitStepWords and not StepKindExternalPlugin — an uncategorized kind)", k))
			continue
		}
		p, ok := providerRegistry.resolve(ClassStep, word)
		if !ok {
			missing = append(missing, fmt.Sprintf("%s (externalized build-emit; class:step plugin %q not registered)", k, word))
			continue
		}
		if _, ok := p.(spec.StepContractCarrier); !ok {
			missing = append(missing, fmt.Sprintf("%s (class:step provider %q declares no StepContract)", k, word))
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("reserved-word registry: unserved InstallStep kinds: %v", missing)
	}
	return nil
}
