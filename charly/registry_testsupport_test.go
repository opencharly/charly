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
// It restores ALL the state a plugin registration mutates: the registry (byKey/origins/closers)
// AND the parse-time pluginPrimaries desugar table AND the process-wide plugin schema set
// (pluginSchemas: sources/inputDefs/unified) that a plugin serving an authored input def fills via
// registerPluginUnitSchema. (S3b: the former deploy-substrate sub-registries — substrateLifecycles,
// deployPreresolvers + pluginPreresolverWords — are deleted; pluginDeployTarget reads
// gp.lifecycle/gp.preresolve directly off the resolved *grpcProvider instead, so there is nothing
// left to snapshot for them.) Only the registry itself duplicate-ERRORS on re-register; the schema
// set replaces/appends idempotently, but restoring it keeps one test's schema registration from
// bleeding into the next. Restoring pluginSchemas ALSO bounds its append-only `sources` slice under
// `-count>N`: without it, a re-registering test re-appends its def on every run — safe only because
// identical defs unify, but the slice (and every recompile of base ++ Σ) grows monotonically. With
// it, each test's schema registration is undone, so the seam does not lean on CUE idempotence.
func snapshotProviderState() func() {
	providerRegistry.mu.Lock()
	byKey := maps.Clone(providerRegistry.byKey)
	origins := maps.Clone(providerRegistry.origins)
	closers := append([]io.Closer(nil), providerRegistry.closers...)
	providerRegistry.mu.Unlock()

	primaries := maps.Clone(pluginPrimaries)

	pluginSchemas.mu.Lock()
	schemaSources := append([]string(nil), pluginSchemas.sources...)
	schemaDefs := maps.Clone(pluginSchemas.inputDefs)
	schemaUnified := pluginSchemas.unified
	pluginSchemas.mu.Unlock()

	return func() {
		providerRegistry.mu.Lock()
		providerRegistry.byKey = byKey
		providerRegistry.origins = origins
		providerRegistry.closers = closers
		providerRegistry.mu.Unlock()

		pluginPrimaries = primaries

		pluginSchemas.mu.Lock()
		pluginSchemas.sources = schemaSources
		pluginSchemas.inputDefs = schemaDefs
		pluginSchemas.unified = schemaUnified
		pluginSchemas.mu.Unlock()
	}
}
