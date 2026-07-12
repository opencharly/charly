package main

import "testing"

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
// after the C1.1–C1.6 build-emit externalization COMPLETED it. The ONE remaining in-proc kind
// (ExternalPlugin — everything NOT in pluginEmitStepWords) lives in its OWN dedicated
// plugin_step_external.go file, self-registers via registerDedicatedBuiltin, and is absent from both
// builtinProviderInstances and the `providers:` manifest — yet resolves through the SAME
// providerRegistry as a typed StepProvider. The 12 kinds in pluginEmitStepWords have NO in-proc
// StepProvider: their build-emit is served by the compiled-in class:step plugin
// candy/plugin-installstep, resolved by its lowercase word with a declared StepContract. The step
// bijection gate in init() checks the same split (a missing provider panics at startup). The test
// fails if any registration regresses.
func TestDedicatedProviders_BulkStepResolveAndAbsent(t *testing.T) {
	byKey := builtinInstanceMap()
	manifest := parseEmbeddedProviderManifest()
	inManifest := func(class ProviderClass, word string) bool {
		for _, w := range manifest[string(class)] {
			if w == word {
				return true
			}
		}
		return false
	}

	for _, kind := range allStepKinds {
		// The 12 plugin-served kinds' build-emit externalized to candy/plugin-installstep: NO in-proc
		// StepProvider; served by a compiled-in class:step plugin (lowercase word, StepContract).
		if word, isPlugin := pluginEmitStepWords[kind]; isPlugin {
			if _, ok := stepProviderFor(kind); ok {
				t.Fatalf("step:%s still has an in-proc StepProvider — its build-emit externalized to candy/plugin-installstep", kind)
			}
			prov, ok := providerRegistry.resolve(ClassStep, word)
			if !ok {
				t.Fatalf("step:%s externalized build-emit word %q not registered (candy/plugin-installstep not compiled in?)", kind, word)
			}
			if _, ok := prov.(stepContractCarrier); !ok {
				t.Fatalf("step:%s class:step provider %q declares no StepContract", kind, word)
			}
			continue
		}
		// Every other kind resolves to a dedicated in-proc StepProvider, absent from the
		// manifest-driven instance supply.
		sp, ok := stepProviderFor(kind)
		if !ok {
			t.Fatalf("stepProviderFor(%q) not resolved — dedicated self-registration regressed", kind)
		}
		if sp.Reserved() != string(kind) {
			t.Fatalf("step:%s Reserved() = %q, want %q", kind, sp.Reserved(), kind)
		}
		if sp.Class() != ClassStep {
			t.Fatalf("step:%s Class() = %q, want %q", kind, sp.Class(), ClassStep)
		}
		if _, in := byKey[provKey(ClassStep, string(kind))]; in {
			t.Fatalf("step:%s is still in builtinProviderInstances — must self-register from its dedicated file", kind)
		}
		if inManifest(ClassStep, string(kind)) {
			t.Fatalf("step:%s is still in the providers: manifest — a dedicated provider must not be listed there", kind)
		}
	}

	// The `providers:` manifest carries NO step entries at all — the in-proc step provider is a
	// dedicated builtin and the 12 externalized kinds are compiled-in plugin candies.
	if len(manifest[string(ClassStep)]) != 0 {
		t.Fatalf("providers: manifest step list = %v, want empty (no manifest-driven step instances)", manifest[string(ClassStep)])
	}
}
