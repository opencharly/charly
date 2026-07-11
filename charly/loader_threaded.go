package main

import "github.com/opencharly/sdk/loaderkit"

// activeLoaderParser is the registered config-front-end PARSE — the loaderkit.DocParser of the
// compiled-in loader plugin (candy/plugin-loader), wired at registration when a provider that
// implements loaderkit.DocParser registers (plugin_inproc.go). Defaults to the built-in
// node-form parse so the bootstrap path (before any loader plugin registers — there is none, the
// compiled-in loader registers at init before the first load) still parses. Swapping the loader
// plugin swaps the config front-end here.
var activeLoaderParser loaderkit.DocParser = loaderkit.DefaultParser{}

// loader_threaded.go — the host snapshot of the registry-derived kind-recognition DATA the
// unified-config PARSE (sdk/loaderkit) consults (P6). The parse is a kind-blind mechanism that
// never queries the provider registry directly (boundary law clause D); the host snapshots which
// words it recognizes into a loaderkit.Threaded and threads it in. Computed fresh per parse pass:
// connectDeclaredKindPlugins runs before the document parse, so the registry is stable within a
// pass, and the re-entrant connect-then-reload re-snapshots.

// loaderThreaded builds the loaderkit.Threaded snapshot: the recognized kind / deploy-substrate
// words (registered providers + parse-time pre-scan declarations), the kinds that may nest
// sub-entity members, and each plugin verb's scalar-sugar primary field.
func loaderThreaded() loaderkit.Threaded {
	t := loaderkit.Threaded{
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
