package main

import (
	"errors"
	"fmt"
	"log"

	"github.com/opencharly/spec/spec"
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
// MaterializeSeams.DecodeEntity/BuildFleetEntity, exactly like WalkSeams' callbacks above.

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

// activeProjectLoader is the registered whole-project LOAD-ENTRY — the spec.ProjectLoader of the
// compiled-in loader plugin (candy/plugin-loader), wired at registration (plugin_inproc.go). It is
// how charly core loads its own charly.yml WITHOUT importing loaderkit (#55 loader-keystone): the
// host drives loaderkit.LoadUnified THROUGH this spec-typed seam, supplying the registry-/host-
// coupled legs as a hostLoaderExecutor. No in-core fallback, mirroring activeProjectWalker: a nil
// loader means the loader plugin was not compiled in — a FATAL, never a silent fallback
// (requireProjectLoader).
var activeProjectLoader spec.ProjectLoader

// requireProjectLoader returns the registered loader or FATALs with a clear message.
func requireProjectLoader() spec.ProjectLoader {
	if activeProjectLoader == nil {
		log.Fatal("no loader plugin registered — charly was built without candy/plugin-loader (the config front-end)")
	}
	return activeProjectLoader
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

// candyVocab is the build VOCABULARY the candy-manifest shape guard consults — the distro/format
// name sets DERIVED at load time from the embedded build vocabulary (plus any project override),
// never a Go constant, so adding a distro or package format stays a vocabulary edit. It is the ONE
// piece of host state parseCandyYAML supplies to the relocated parse mechanism; the mechanism itself
// (and the sets' own membership rule) live in sdk/loaderkit + spec.CandyVocab since K-wave 2 cone R1
// (A2 unit 2). The zero value fails the guard OPEN — no false positives — matching the pre-move
// contract where an unregistered vocabulary cleared the caches.
var candyVocab spec.CandyVocab

// RegisterBuildVocabulary derives the distro/format vocabulary from a DistroConfig and caches it for
// the duration of the process. Safe to call repeatedly; a nil config clears it.
func RegisterBuildVocabulary(dc *spec.DistroConfig) { candyVocab = spec.NewCandyVocab(dc) }

// parseCandyYAML is the candy-MANIFEST parse the two CandyScanner scan methods take as their
// parseManifest seam. It is a bare forward into the seam THIS file declares: the mechanism relocated
// to sdk/loaderkit in K-wave 2 cone R1 (A2 unit 2) so a plugin driving its own scan can parse
// manifests itself, and charly/ may not import sdk/loaderkit (import purity), so core reaches it
// through the registered scanner. All core supplies is the two host-side values the mechanism takes
// as parameters — the registry-derived kind-recognition snapshot and the build vocabulary above.
//
// Clause B is NOT what keeps this here, and the distinction matters: an RDD spike over the whole
// 324-manifest corpus proved the pre-move node-form branch's pn->genericNode->buildCandy->pn round
// trip was an IDENTITY (321 node-form manifests plus all 3 error paths, byte-identical), so the
// bootstrap-critical factory was never on this path. buildCandy/candyIsImage stay core for their
// GENUINE clause-B consumers — the discovered-candy pre-check and foldCandyKind.
func parseCandyYAML(path string) (*spec.CandyYAML, error) {
	return requireCandyScanner().ParseCandyManifest(path, loaderThreaded(), candyVocab)
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
// composes it ITSELF (sdk/spec.RepoIdentity / RootRepoIdentity, defaulted in
// candy/plugin-loader's WalkProject when the host leaves these zero) rather than charly core
// holding that logic just to thread a function value through a struct literal.
func hostWalkProject(dir string, rootData []byte) (spec.LoadedProject, error) {
	return requireProjectWalker().WalkProject(dir, rootData, "", hostWalkSeams())
}

// hostWalkSeams builds the spec.WalkSeams every registry-coupled walk-level entry point threads
// through: the whole-project walk (hostWalkProject, above) AND the standalone discover-only walk
// (ApplyDiscover, below) share this ONE construction (R3) rather than each re-deriving it.
//
// Only Boundary and Threaded are genuinely host-side (they touch the provider registry). The other
// three are one-line forwards into seams this file declares: ResolveRef's import-ref resolution
// mechanism relocated to sdk/loaderkit alongside the fetch it drives (K-wave 2 cone R1 unit 3 —
// charly/unified.go's canonicalRef, deleted), reached here through the loader plugin exactly like
// ResolveProjectRepo below.
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
		Threaded: loaderThreaded,
		ResolveRef: func(ref, baseDir string) (string, string, error) {
			return requireProjectLoader().CanonicalRef(hostInProcCtx(), ref, baseDir)
		},
		GateDoc: requireProjectLoader().ValidateNodeDocCUE,
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
	// (the SAME deployTraitsFor the loader's per-node descent stamp, loaderkit.StampFleetDescents, calls) so the
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
		BuildFleetEntity:         buildFleetEntityViaRegistry,
		InKindConnectPass:        inKindConnectPass,
		DeclaredKindConnectError: declaredKindConnectError,
	}
}

// decodeEntityViaRegistry implements spec.MaterializeSeams.DecodeEntity: resolves pn's
// discriminator against the provider registry and, if found, dispatches via the SAME runPluginKind
// the former in-core normalizeNodeInto called directly (provider_kind_invoke.go — the TRUE
// clause-M mechanism, unchanged). Threads pn straight into the dispatch (K1 unit 3b) — the former
// genericNode reconstruction is gone from this path entirely; runPluginKind's own tree-assembly
// calls (buildFleetNode/assembleEntityBody/…) now route through the ProjectLoader seam on pn
// directly, and genericNode survives ONLY where foldCandyKind needs it for the bootstrap-critical
// candyIsImage/buildCandy routing. found=false (no error) means no provider resolves pn.Disc; the
// registered Materializer plugin applies its own not-found policy from there.
func decodeEntityViaRegistry(pn spec.ParsedNode, acc *spec.MaterializedProject) (bool, error) {
	prov, ok := providerRegistry.ResolveKind(pn.Disc)
	if !ok {
		return false, nil
	}
	return true, runPluginKind(prov, pn, acc)
}

// buildFleetEntityViaRegistry implements spec.MaterializeSeams.BuildFleetEntity: the fallback for
// a recognized-but-not-yet-connected external deploy substrate word, mirroring the former in-core
// normalizeNodeInto's recognizedDeploySubstrate branch — now the relocated
// sdk/loaderkit.BuildFleetNodeInto (K1 unit 3b), reached through the ProjectLoader seam with pn
// threaded straight through (no genericNode reconstruction).
func buildFleetEntityViaRegistry(pn spec.ParsedNode, acc *spec.MaterializedProject) error {
	return requireProjectLoader().BuildFleetNodeInto(pn, loaderThreaded(), acc)
}

// -----------------------------------------------------------------------------
// The project LOAD-ENTRY forwards.
//
// K-wave 2 cone R1 COLOCATED these here from charly/config.go, charly/format_config.go and
// charly/unified.go, all three deleted. Each was a file whose entire remaining content was a
// one-or-two-line forward into the ProjectLoader seam declared above — the seam is the owner, so the
// forwards live with it. They are deliberately NOT inlined into their ~40 call sites: that would
// have GROWN the kernel to make three files disappear, which is the cosmetic-gaming pattern the
// residue ledger forbids. What genuinely carried LOGIC left instead: ResolveProjectRepo (the --repo
// clone-and-cache) and the candy-scan projection both moved into candy/plugin-loader.
// -----------------------------------------------------------------------------

// ErrNoCharlyYml is the sentinel wrapped by every "no charly.yml found in the project dir" load
// error. Callers that treat an absent project as EMPTY rather than a hard failure (the
// `charly box list …` read commands — an empty project has zero boxes, like `ls` in an empty dir)
// match it with errors.Is.
var ErrNoCharlyYml = errors.New("no charly.yml found in project directory")

// noCharlyYmlErr is the ONE construction of the absent-project load error, wrapping ErrNoCharlyYml
// for errors.Is.
func noCharlyYmlErr(dir string) error {
	return fmt.Errorf("no charly.yml found in %s (run `charly box new project .` to scaffold one): %w", dir, ErrNoCharlyYml)
}

// LoadUnified drives the whole-project load through the registered spec.ProjectLoader seam. The host
// passes its own hostLoaderExecutor{} (the typed spec.LoaderExecutor reaching each registry-/
// host-coupled load step by calling the host function DIRECTLY — zero marshal, a compiled-in TYPED
// placement pays no envelope tax); the COMPILED-IN candy/plugin-loader implements spec.ProjectLoader
// and internally runs loaderkit.LoadUnified. The seam is registered at init (before main), so the
// host resolves it before loading its own charly.yml — no bootstrap cycle. charly core holds only
// the seam interface + the host executor legs; the kind-blind orchestration (bootstrap phase, schema
// gates, walk, materialize, venue flatten, member fold, descent stamp, the validation chain) lives
// in loaderkit, driven by the plugin.
func LoadUnified(dir string) (*spec.UnifiedFile, bool, error) {
	return requireProjectLoader().LoadUnified(dir, hostLoaderExecutor{})
}

// LoadConfig reads charly.yml and returns the spec.Config (defaults + boxes) projection. Mode purity
// preserved: this reads the PROJECT charly.yml only and never merges the per-host charly.yml overlay.
// Deploy-mode commands must call LoadFleetConfig + MergeDeployOntoMetadata explicitly.
func LoadConfig(dir string) (*spec.Config, error) {
	uf, present, err := LoadUnified(dir)
	if err != nil {
		return nil, fmt.Errorf("loading charly.yml: %w", err)
	}
	if !present {
		return nil, noCharlyYmlErr(dir)
	}
	return uf.ProjectConfig(), nil
}

// LoadBuildConfigForBox loads the distro, builder and init vocabularies for the project at dir, via
// the same unified load. The init section is optional: a project without one yields a nil
// *spec.InitConfig (no init system, no entrypoint beyond the base image default). The projections
// themselves live in loaderkit (the ONE home charly core and candy/plugin-build both call, R3);
// charly supplies its in-proc registry OpResolve callbacks for the opaque distro/init bodies, while
// the builder bodies decode purely.
func LoadBuildConfigForBox(dir string) (*spec.DistroConfig, *spec.BuilderConfig, *spec.InitConfig, error) {
	uf, present, err := LoadUnified(dir)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("loading charly.yml: %w", err)
	}
	if !present {
		return nil, nil, nil, noCharlyYmlErr(dir)
	}
	return spec.ProjectDistroConfig(uf, resolveDistroViaPlugin),
		spec.ProjectBuilderConfig(uf),
		spec.ProjectInitConfig(uf, resolveInitConfigViaPlugin), nil
}

// LoadDefaultBuildConfig is retained as the single-argument alias of the above.
func LoadDefaultBuildConfig(dir string) (*spec.DistroConfig, *spec.BuilderConfig, *spec.InitConfig, error) {
	return LoadBuildConfigForBox(dir)
}

// ResolveProjectRepo forwards the `--repo` clone-and-cache resolve to the seam. The LOGIC (spec
// normalization, default-branch resolve, the fetch) relocated into candy/plugin-loader with the rest
// of the fetch orchestration; charly/main_repo.go is deleted.
func ResolveProjectRepo(repoSpec string) (string, error) {
	return requireProjectLoader().ResolveProjectRepo(hostInProcCtx(), repoSpec)
}

// ApplyDiscover walks every flat scan spec on uf.Discover and registers each entity it finds. Each
// spec scans its path for directories containing that spec's manifest; every discovered manifest is
// routed by SHAPE, and an explicit map entry always wins over a discovered one. scanRoot resolution
// is relative to rootDir (the dir containing charly.yml).
//
// The WALK+PARSE half (find directories, read + classify + gate + parse each manifest's documents)
// is loaderkit.RunDiscover, reached through the seam this file declares — the SAME mechanism the
// whole-project walk's own depth-0 discover pass drives internally, reused rather than duplicated.
// Only the registry-coupled MATERIALIZE fold (foldDiscoveredManifests, materialize.go — shared with
// the walk's discovered-manifest step via the FoldDiscoveredManifests seam, R3) is host-side.
func ApplyDiscover(uf *spec.UnifiedFile, rootDir string) error {
	dms, err := requireProjectLoader().RunDiscover(rootDir, uf.Discover, hostWalkSeams())
	if err != nil {
		return err
	}
	return foldDiscoveredManifests(dms, uf)
}

// projectCandiesScanned scans or synthesizes a candy per entry in uf.Candy, into its pre-completion,
// pre-finalize spec.ScannedCandy form. Entries with `from:` go through the registered CandyScanner
// seam so directory-based candies behave identically; inline entries synthesize from the embedded
// CandyYAML and take their DECLARING FILE's directory as SourceDir — rootDir here, or the namespace
// sub-file's dir when one declares them (see ScanInlineCandy's own contract).
func projectCandiesScanned(uf *spec.UnifiedFile, rootDir string) (map[string]spec.ScannedCandy, error) {
	return requireCandyScanner().ProjectCandiesScanned(uf, rootDir, parseCandyYAML)
}
