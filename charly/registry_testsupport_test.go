package main

import (
	"io"
	"maps"
)

// snapshotProviderState captures the process-wide provider-registration state and returns a
// restore func. The provider registry (`providerRegistry`) is a package global, so a test that
// registers providers (RegisterPluginProviders / RegisterBuiltinProvider / registerDedicatedBuiltin)
// LEAKS them into every later test in the process — and under `go test -count>1` the SECOND run of
// the same test hits `register()`'s fail-fast "provider already registered" duplicate guard. A test
// that registers providers calls
//
//	t.Cleanup(snapshotProviderState())
//
// as its FIRST line, so its registrations are undone afterward and the test is hermetic under
// `-count>1` (which the concurrency-stress gate `go test -race -count=N ./charly/...` needs).
//
// It restores ALL the state RegisterPluginProviders mutates: the registry (byKey/origins/closers)
// AND the deploy-substrate sub-registries a class:deploy plugin also wires (substrateLifecycles,
// deployPreresolvers + pluginPreresolverWords) AND the parse-time pluginPrimaries desugar table.
// Only the registry itself duplicate-ERRORS on re-register; the sub-registries replace idempotently,
// but restoring them keeps one test's substrate/preresolver/primary from bleeding into the next.
func snapshotProviderState() func() {
	providerRegistry.mu.Lock()
	byKey := maps.Clone(providerRegistry.byKey)
	origins := maps.Clone(providerRegistry.origins)
	closers := append([]io.Closer(nil), providerRegistry.closers...)
	providerRegistry.mu.Unlock()

	substrateLifecyclesMu.Lock()
	subLife := maps.Clone(substrateLifecycles)
	substrateLifecyclesMu.Unlock()

	deployPreresolversMu.Lock()
	preres := maps.Clone(deployPreresolvers)
	preresWords := maps.Clone(pluginPreresolverWords)
	deployPreresolversMu.Unlock()

	primaries := maps.Clone(pluginPrimaries)

	return func() {
		providerRegistry.mu.Lock()
		providerRegistry.byKey = byKey
		providerRegistry.origins = origins
		providerRegistry.closers = closers
		providerRegistry.mu.Unlock()

		substrateLifecyclesMu.Lock()
		substrateLifecycles = subLife
		substrateLifecyclesMu.Unlock()

		deployPreresolversMu.Lock()
		deployPreresolvers = preres
		pluginPreresolverWords = preresWords
		deployPreresolversMu.Unlock()

		pluginPrimaries = primaries
	}
}
