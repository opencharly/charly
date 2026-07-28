package main

import (
	"log"

	"github.com/opencharly/sdk/spec"
)

// loader_threaded.go — the host side of the unified-config loader seam (P6/K1/#46). It holds the
// registered per-document PARSER (activeLoaderParser), the registered whole-project WALKER
// (activeProjectWalker), the registered per-node kind-decode MATERIALIZER (activeMaterializer,
// K1 unit 1), and builds the registry-derived kind-recognition snapshot (loaderThreaded) the
// parse/materialize consult instead of querying the provider registry directly (boundary law
// clause D). The seam CONTRACT types (spec.DocParser / spec.Threaded / spec.WalkSeams /
// spec.ProjectWalker / spec.MaterializeSeams / spec.Materializer) live in sdk/spec so neither the
// host nor the loader plugin imports the other — charly core imports NEITHER loaderkit NOR any
// other sdk mechanism kit; the WALK mechanism (loaderkit.Walk) and the per-node MATERIALIZE POLICY
// (loaderkit.Materialize) are reached exclusively through the compiled-in loader plugin's typed
// ProjectWalker / Materializer, resolved here. The ACTUAL registry resolve + provider dispatch a
// materialize pass performs (clause M) never leaves this file — it is threaded to the plugin via
// MaterializeSeams.DecodeEntity/BuildBundleEntity, exactly like WalkSeams' callbacks above.

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
	return requireProjectWalker().WalkProject(dir, rootData, "", hostWalkSeams())
}

// hostWalkSeams builds the spec.WalkSeams every registry-coupled walk-level entry point threads
// through: the whole-project walk (hostWalkProject, above) AND the standalone discover-only walk
// (ApplyDiscover, unified.go — the K1 keystone task #24 unit-3 discover seam) share this ONE
// construction (R3) rather than each re-deriving it.
func hostWalkSeams() spec.WalkSeams {
	return spec.WalkSeams{
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
}

// loaderThreaded builds the spec.Threaded snapshot: the recognized kind / deploy-substrate words
// (registered providers + parse-time pre-scan declarations), the kinds that may nest sub-entity
// members, and each plugin verb's scalar-sugar primary field. Computed fresh per parse pass:
// connectDeclaredKindPlugins runs before the document parse, so the registry is stable within a
// pass, and the re-entrant connect-then-reload re-snapshots.
func loaderThreaded() spec.Threaded {
	t := spec.Threaded{
		Kinds:                    map[string]bool{},
		DeploySubstrates:         map[string]bool{},
		StructuralKinds:          map[string]bool{},
		Primaries:                map[string]string{},
		DeployTraits:             map[string]*spec.DeployTraits{},
		ExternalDeploySubstrates: map[string]bool{},
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
	// its out-of-process provider connects — recognizedDeploySubstrate; the kind-word analogue is
	// this same declaredKind loop below, now consumed as the spec.Threaded.Kinds DATA snapshot
	// rather than through a dedicated recognizedKind() function).
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
	// K1-LOADER RELOCATION: snapshot each recognized kind/substrate word's DECLARED #DeployTraits
	// (the SAME deployTraitsFor the loader's per-node descent stamp, loaderkit.StampBundleDescents, calls) so the
	// venue-hop descent stamp reads DATA, never the registry. A word whose deployTraitsFor is nil
	// (a non-substrate kind, e.g. group/distro) is left absent — the DATA closure returns nil for
	// it, matching deployTraitsFor's nil-for-unrecognized-word semantics via DescentFromTraits(nil).
	for k := range t.Kinds {
		if tr := deployTraitsFor(k); tr != nil {
			t.DeployTraits[k] = tr
		}
	}
	for k := range t.DeploySubstrates {
		if tr := deployTraitsFor(k); tr != nil {
			t.DeployTraits[k] = tr
		}
	}
	// K1-LOADER RELOCATION: snapshot the EXACT set of words the host's registry-live
	// isExternalDeploySubstrate accepts, evaluated over every recognized substrate word (the
	// isExternalDeploySubstrate=true set is a subset of the recognized substrates, since it requires
	// recognizedDeploySubstrate). loaderkit.ValidateCheckBeds checks membership here instead of
	// reconstructing the resourceKindSet ∧ externalizedDeploySubstrates ∧ recognizedDeploySubstrate
	// decision — the byte-exact host predicate is threaded, never approximated.
	for w := range t.DeploySubstrates {
		if isExternalDeploySubstrate(w) {
			t.ExternalDeploySubstrates[w] = true
		}
	}
	return t
}

// activeMaterializer is the registered per-node kind-decode DISPATCH POLICY — the spec.Materializer
// of the compiled-in loader plugin (candy/plugin-loader), wired at registration (plugin_inproc.go).
// No in-core fallback, mirroring activeProjectWalker: a nil materializer means the loader plugin
// was not compiled in — a FATAL, never a silent fallback (requireMaterializer). K1 unit 1.
var activeMaterializer spec.Materializer

// requireMaterializer returns the registered materializer or FATALs with a clear message.
func requireMaterializer() spec.Materializer {
	if activeMaterializer == nil {
		log.Fatal("no loader plugin registered — charly was built without candy/plugin-loader (the config front-end)")
	}
	return activeMaterializer
}

// hostMaterializeSeams builds the MaterializeSeams the registered Materializer plugin calls back
// into for the actually registry-coupled dispatch: resolving a parsed node's discriminator against
// the provider registry and invoking the resolved Provider (clause M — provider_registry.go /
// provider_kind_invoke.go are the TRUE mechanism; this file never redefines it, only threads it
// through the seam, exactly like hostWalkProject's Boundary/ResolveRef/GateDoc callbacks above).
func hostMaterializeSeams() spec.MaterializeSeams {
	return spec.MaterializeSeams{
		DecodeEntity:             decodeEntityViaRegistry,
		BuildBundleEntity:        buildBundleEntityViaRegistry,
		InKindConnectPass:        inKindConnectPass,
		DeclaredKindConnectError: declaredKindConnectError,
	}
}

// decodeEntityViaRegistry implements spec.MaterializeSeams.DecodeEntity: resolves pn's
// discriminator against the provider registry and, if found, dispatches via the SAME runPluginKind
// the former in-core normalizeNodeInto called directly (provider_kind_invoke.go — the TRUE
// clause-M mechanism, unchanged). Reconstructs the genericNode that dispatch reads from pn (mirrors
// the former materializeParsedNode/parsedNodeToGeneric pairing). found=false (no error) means no
// provider resolves pn.Disc; the registered Materializer plugin applies its own not-found policy
// from there.
func decodeEntityViaRegistry(pn spec.ParsedNode, acc *spec.MaterializedProject) (bool, error) {
	prov, ok := providerRegistry.ResolveKind(pn.Disc)
	if !ok {
		return false, nil
	}
	gn, err := parsedNodeToGeneric(pn)
	if err != nil {
		return true, err
	}
	return true, runPluginKind(prov, gn, acc)
}

// buildBundleEntityViaRegistry implements spec.MaterializeSeams.BuildBundleEntity: the fallback for
// a recognized-but-not-yet-connected external deploy substrate word, mirroring the former in-core
// normalizeNodeInto's recognizedDeploySubstrate branch (buildBundleNodeInto, node_bundle.go).
func buildBundleEntityViaRegistry(pn spec.ParsedNode, acc *spec.MaterializedProject) error {
	gn, err := parsedNodeToGeneric(pn)
	if err != nil {
		return err
	}
	return buildBundleNodeInto(gn, acc)
}
