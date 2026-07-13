package main

import (
	"log"

	"github.com/opencharly/sdk/spec"
)

// loader_threaded.go — the host side of the unified-config loader seam (P6/K1). It holds the
// registered per-document PARSER (activeLoaderParser) and builds the registry-derived
// kind-recognition snapshot (loaderThreaded) the parse consults instead of querying the provider
// registry directly (boundary law clause D). The seam CONTRACT types (spec.DocParser / spec.Threaded)
// live in sdk/spec so neither the host nor the loader plugin imports the other; the WALK mechanism
// (loaderkit.Walk) is reached through the single loader_driver.go import.

// activeLoaderParser is the registered config-front-end PARSE — the spec.DocParser of the
// compiled-in loader plugin (candy/plugin-loader), wired at registration (plugin_inproc.go). There
// is NO in-core fallback (K1 deleted loaderkit.DefaultParser): the compiled-in loader registers at
// init before the first load, so a nil parser means the loader plugin was not compiled in — a
// FATAL, never a silent fallback (requireLoaderParser).
var activeLoaderParser spec.DocParser

// requireLoaderParser returns the registered parser or FATALs with a clear message. Every parse
// site (the Walk driver + the box-validate node-form parse + the layers candy scan) goes through
// it, so a missing loader plugin fails loudly and identically everywhere.
func requireLoaderParser() spec.DocParser {
	if activeLoaderParser == nil {
		log.Fatal("no loader plugin registered — charly was built without candy/plugin-loader (the config front-end)")
	}
	return activeLoaderParser
}

// loaderThreaded builds the spec.Threaded snapshot: the recognized kind / deploy-substrate words
// (registered providers + parse-time pre-scan declarations), the kinds that may nest sub-entity
// members, and each plugin verb's scalar-sugar primary field. Computed fresh per parse pass:
// connectDeclaredKindPlugins runs before the document parse, so the registry is stable within a
// pass, and the re-entrant connect-then-reload re-snapshots.
func loaderThreaded() spec.Threaded {
	t := spec.Threaded{
		Kinds:            map[string]bool{},
		DeploySubstrates: map[string]bool{},
		StructuralKinds:  map[string]bool{},
		Primaries:        map[string]string{},
	}
	for _, p := range providerRegistry.allProviders() {
		switch p.Class() {
		case ClassKind:
			t.Kinds[p.Reserved()] = true
		case ClassDeployTarget:
			t.DeploySubstrates[p.Reserved()] = true
		}
	}
	// Parse-time pre-scan declarations (a project plugin's kind/substrate word recognized before
	// its out-of-process provider connects — recognizedKind/recognizedDeploySubstrate).
	declaredDeployMu.RLock()
	for k := range declaredKind {
		t.Kinds[k] = true
	}
	for k := range declaredDeploySubstrate {
		t.DeploySubstrates[k] = true
	}
	declaredDeployMu.RUnlock()
	// Member-nesting: any recognized kind/substrate externalKindMayNestMembers accepts (the
	// resource kinds pod/vm/… are handled by loaderkit's spec-vocab resourceKindSet directly).
	for k := range t.Kinds {
		if externalKindMayNestMembers(k) {
			t.StructuralKinds[k] = true
		}
	}
	for k := range t.DeploySubstrates {
		if externalKindMayNestMembers(k) {
			t.StructuralKinds[k] = true
		}
	}
	for w, f := range pluginPrimaries {
		t.Primaries[w] = f
	}
	return t
}
