package main

import (
	"testing"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/spec/spec"
)

// TestExternalizedBuilders_NoInProcProvider proves the four detection-builders (cargo/npm/pixi/aur)
// are EXTERNAL out-of-process plugin candies with NO compiled-in BuilderProvider — the builder
// analogue of TestReservedWordRegistry_DeployBijection. At process start (before any plugin
// connects at load time) the registry resolves NONE of them, and each is recorded in the
// externalizedBuilders set with a serving plugin candy in externalBuilderPlugins. A regression
// that re-introduced an in-proc builtin builder would resolve here and fail.
func TestExternalizedBuilders_NoInProcProvider(t *testing.T) {
	t.Cleanup(snapshotProviderState())
	byKey := builtinInstanceMap()
	manifest := parseEmbeddedProviderManifest()
	for _, word := range []string{"cargo", "npm", "pixi", "aur"} {
		if !externalizedBuilders[word] {
			t.Fatalf("builder %q must be in externalizedBuilders (single source of truth)", word)
		}
		if _, ok := externalBuilderPlugins[word]; !ok {
			t.Fatalf("builder %q must name its serving plugin candy in externalBuilderPlugins", word)
		}
		if _, ok := providerRegistry.resolve(ClassBuilder, word); ok {
			t.Fatalf("builder %q resolves to an in-proc provider at process start — it must be external (connected only at plugin-load time)", word)
		}
		if _, in := byKey[provKey(ClassBuilder, word)]; in {
			t.Fatalf("builder %q is in builtinProviderInstances — an externalized builder has no compiled-in provider", word)
		}
		for _, w := range manifest[string(ClassBuilder)] {
			if w == word {
				t.Fatalf("builder %q is in the providers: manifest — an externalized builder has no compiled-in provider", word)
			}
		}
		if ref, ok := externalBuilderPluginRef(word); !ok || ref == "" {
			t.Fatalf("builder %q must produce a canonical plugin ref for submodule auto-inject", word)
		}
	}
}

// TestDedicatedProviders_BulkStepResolveAndAbsent proves how every InstallStep kind is SERVED
// after K5-A item 2 relocated the LAST per-kind dispatch decision out of core. The 12 kinds in
// pluginEmitStepWords have NO in-proc StepProvider (that category no longer exists at all — the
// former StepProvider interface/stepProviderFor/builtinStepBase are deleted, R1: zero live callers
// once ExternalPlugin's EmitOCI dispatch relocated too): their build-emit is served by the
// compiled-in class:step plugin candy/plugin-installstep, resolved by its lowercase word with a
// declared StepContract. The ONE remaining kind (ExternalPlugin) has NO ClassStep registry entry
// at all anymore — its build-emit is an unconditional Go-level type-switch arm in
// candy/plugin-installstep's "oci-dispatch" word (dispatchExternalPluginVerb), which resolves its
// OWN class:verb target directly via InvokeProvider, bypassing the (now-deleted)
// externalPluginStepProvider/ClassStep "ExternalPlugin" registration entirely. The bijection gate
// (checkStepProviderBijection, provider_step.go) verifies the SAME split at startup. The test
// fails if any registration regresses.
func TestDedicatedProviders_BulkStepResolveAndAbsent(t *testing.T) {
	manifest := parseEmbeddedProviderManifest()

	for _, kind := range deploykit.AllStepKinds {
		if kind == deploykit.StepKindExternalPlugin {
			// No ClassStep registry entry exists (or is needed) for this kind — verified structurally
			// by the bijection gate + the plugin-side Go-level switch, not a runtime registry lookup.
			if _, ok := providerRegistry.resolve(ClassStep, string(kind)); ok {
				t.Fatalf("step:%s unexpectedly resolves a ClassStep provider — the in-proc StepProvider category was deleted (K5-A item 2)", kind)
			}
			continue
		}
		// The 12 plugin-served kinds' build-emit externalized to candy/plugin-installstep: served by
		// a compiled-in class:step plugin (lowercase word, StepContract).
		word, isPlugin := pluginEmitStepWords[kind]
		if !isPlugin {
			t.Fatalf("step:%s is neither StepKindExternalPlugin nor in pluginEmitStepWords — an uncategorized kind", kind)
		}
		prov, ok := providerRegistry.resolve(ClassStep, word)
		if !ok {
			t.Fatalf("step:%s externalized build-emit word %q not registered (candy/plugin-installstep not compiled in?)", kind, word)
		}
		if _, ok := prov.(spec.StepContractCarrier); !ok {
			t.Fatalf("step:%s class:step provider %q declares no StepContract", kind, word)
		}
	}

	// The `providers:` manifest carries NO step entries at all — the 12 externalized kinds are
	// served by the compiled-in candy/plugin-installstep, never a manifest-driven instance.
	if len(manifest[string(ClassStep)]) != 0 {
		t.Fatalf("providers: manifest step list = %v, want empty (no manifest-driven step instances)", manifest[string(ClassStep)])
	}
}
