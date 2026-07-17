package main

import (
	"log"

	"github.com/opencharly/sdk/spec"
)

// loader_threaded.go — the host side of the unified-config loader seam (P6/K1/#46). It holds the
// registered per-document PARSER (activeLoaderParser), the registered whole-project WALKER
// (activeProjectWalker), and builds the registry-derived kind-recognition snapshot (loaderThreaded)
// the parse consults instead of querying the provider registry directly (boundary law clause D).
// The seam CONTRACT types (spec.DocParser / spec.Threaded / spec.WalkSeams / spec.ProjectWalker)
// live in sdk/spec so neither the host nor the loader plugin imports the other — charly core
// imports NEITHER loaderkit NOR any other sdk mechanism kit; the WALK mechanism (loaderkit.Walk) is
// reached exclusively through the compiled-in loader plugin's typed ProjectWalker, resolved here.

// activeLoaderParser is the registered config-front-end PARSE — the spec.DocParser of the
// compiled-in loader plugin (candy/plugin-loader), wired at registration (plugin_inproc.go). There
// is NO in-core fallback (K1 deleted loaderkit.DefaultParser): the compiled-in loader registers at
// init before the first load, so a nil parser means the loader plugin was not compiled in — a
// FATAL, never a silent fallback (requireLoaderParser).
var activeLoaderParser spec.DocParser

// requireLoaderParser returns the registered parser or FATALs with a clear message. Every parse
// site (the walk driver below + the box-validate node-form parse + the layers candy scan) goes
// through it, so a missing loader plugin fails loudly and identically everywhere.
func requireLoaderParser() spec.DocParser {
	if activeLoaderParser == nil {
		log.Fatal("no loader plugin registered — charly was built without candy/plugin-loader (the config front-end)")
	}
	return activeLoaderParser
}

// activeProjectWalker is the registered whole-project WALK — the spec.ProjectWalker of the
// compiled-in loader plugin (candy/plugin-loader), wired at registration (plugin_inproc.go). No
// in-core fallback, mirroring activeLoaderParser: a nil walker means the loader plugin was not
// compiled in — a FATAL, never a silent fallback (requireProjectWalker).
var activeProjectWalker spec.ProjectWalker

// requireProjectWalker returns the registered walker or FATALs with a clear message.
func requireProjectWalker() spec.ProjectWalker {
	if activeProjectWalker == nil {
		log.Fatal("no loader plugin registered — charly was built without candy/plugin-loader (the config front-end)")
	}
	return activeProjectWalker
}

// activeCandyScanner is the registered CANDY-SCAN — the spec.CandyScanner of the compiled-in
// loader plugin (candy/plugin-loader), wired at registration (plugin_inproc.go). No in-core
// fallback, mirroring activeProjectWalker: a nil scanner means the loader plugin was not compiled
// in — a FATAL, never a silent fallback (requireCandyScanner).
var activeCandyScanner spec.CandyScanner

// requireCandyScanner returns the registered scanner or FATALs with a clear message.
func requireCandyScanner() spec.CandyScanner {
	if activeCandyScanner == nil {
		log.Fatal("no loader plugin registered — charly was built without candy/plugin-loader (the config front-end)")
	}
	return activeCandyScanner
}

// hostWalkProject runs the kind-blind whole-project WALK via the registered loader plugin,
// returning its generic parse envelope. rootData is the (bootstrap-transformed) root charly.yml
// bytes; the seams are the REGISTRY-COUPLED host primitives the walk consults instead of the
// provider registry directly (boundary law clause D). This is the SOLE call site that reaches the
// loader plugin's WalkProject — charly core builds the seams from its own host functions but never
// imports sdk/loaderkit to drive the walk itself.
//
// RepoIdentity + the root-identity seed are DELIBERATELY left unset here: that logic (the
// import-namespace cycle-break) is pure fs/git/yaml — no registry coupling — so the loader plugin
// composes it ITSELF (sdk/loaderkit.RepoIdentity / RootRepoIdentity, defaulted in
// candy/plugin-loader's WalkProject when the host leaves these zero) rather than charly core
// holding that logic just to thread a function value through a struct literal.
func hostWalkProject(dir string, rootData []byte) (spec.LoadedProject, error) {
	seams := spec.WalkSeams{
		Parser: requireLoaderParser(),
		// Boundary: the depth-0 parse pre-scan + connect-declared-kind-plugins registry side effects
		// (prescanDeclaredPluginWords + connectDeclaredKindPlugins), run at the root file AND each
		// namespace root before that boundary's documents parse.
		Boundary: func(bdir string, data []byte) error {
			prescanDeclaredPluginWords(data, bdir)
			connectDeclaredKindPlugins(bdir)
			return nil
		},
		Threaded:   loaderThreaded,
		ResolveRef: canonicalRef,
		GateDoc:    validateNodeDocCUE,
	}
	return requireProjectWalker().WalkProject(dir, rootData, "", seams)
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
