package spec

import (
	"context"
	"encoding/json"
	"reflect"

	"cuelang.org/go/cue"
	"gopkg.in/yaml.v3"
)

// loader_seam.go — the hand-written CONTRACT types for the unified-config loader seam (K1/#46).
// These are interface + data contracts (no mechanism): the parse + walk machinery lives in
// sdk/loaderkit, and — since K1 unit 1 — the per-node kind-decode MATERIALIZE dispatch POLICY
// lives there too (Materializer below; the ACTUAL registry resolve + provider dispatch stays
// host-side, clause M, reached through MaterializeSeams exactly like WalkSeams above). They live
// in spec (the shared contract home, alongside the CUE-generated #ParsedProject / #LoadedProject
// wire types) so BOTH the loader plugin (candy/plugin-loader → loaderkit) and the host may
// reference them without either importing the other — charly core imports NEITHER loaderkit NOR
// any other sdk mechanism kit; it reaches the whole-project WALK exclusively through the typed
// ProjectWalker seam below, and the per-node MATERIALIZE exclusively through Materializer, both
// resolved from the registered compiled-in loader plugin (mirroring DocParser/Threaded for PARSE).

// Threaded is the host-computed, registry-derived DATA the per-document parse consults instead of
// querying the provider registry (boundary law clause D): which words are recognized kinds /
// deploy substrates, which external kinds may nest sub-entity members, and each plugin verb's
// scalar-sugar primary field. The host snapshots it before the parse; the parse never touches the
// registry.
type Threaded struct {
	Kinds            map[string]bool   // recognizedKind
	DeploySubstrates map[string]bool   // recognizedDeploySubstrate
	StructuralKinds  map[string]bool   // externalKindMayNestMembers
	Primaries        map[string]string // pluginPrimaryFor: verb word → scalar-sugar primary field
	// DeployTraits is the substrate word → DECLARED #DeployTraits map (K1-LOADER RELOCATION): the
	// descent stamp reads THIS registry-derived DATA snapshot instead of querying the provider
	// registry live, so the venue-hop descent stamp (loaderkit.StampFleetDescents) is registry-free
	// and can run plugin-side. The host fills it (deployTraitsFor per recognized kind/substrate word)
	// exactly as the former live stampFleetDescents did; a word absent from the map resolves to the
	// external-in-place default, matching deployTraitsFor's nil-for-unrecognized-word semantics.
	DeployTraits map[string]*DeployTraits
	// ExternalDeploySubstrates is the EXACT set of words for which the host's registry-live
	// isExternalDeploySubstrate returns true (K1-LOADER RELOCATION): the host fills it by EVALUATING
	// its own isExternalDeploySubstrate over every recognized substrate word, so a plugin-side
	// validator (loaderkit.ValidateCheckBeds) checks membership here instead of RECONSTRUCTING the
	// host's resourceKindSet ∧ externalizedDeploySubstrates ∧ recognizedDeploySubstrate decision —
	// the byte-exact predicate is threaded, never approximated. A word absent from the set is
	// NOT an external deploy substrate (isExternalDeploySubstrate would return false for it).
	ExternalDeploySubstrates map[string]bool
}

// The former CueSchema handle type is GONE (K-wave 2, cone R1, ruling 1). The compiled CUE schema
// and its kind→def table are the LOADER's own possession now (sdk/loaderkit/cue_schema.go), not a
// kernel D-datum the host threads in: the loader is the only consumer of the schema it validates
// against, so under the boundary law the schema travels with that capability (clause R). The six
// CUE-validate methods below therefore take no schema parameter. charly core keeps an
// independently-compiled copy solely for the two kernel mechanisms that genuinely need one — the
// plugin-schema splice and the structural-kind value gate — and the two never interoperate.

// DocParser is the swappable per-document PARSE seam: the loader plugin candy implements it
// (candy/plugin-loader, delegating to loaderkit.ParseDoc), and the host resolves the registered
// loader provider to it and calls it for every config document — so an alternative loader plugin
// serves a different config front-end by implementing this. Typed (no wire envelope) since it runs
// on every document load. `directives` is the reserved-directive mapping (import/discover/version/
// repo/defaults/provides); `pp` is the decomposed entity nodes.
type DocParser interface {
	ParseDoc(doc *yaml.Node, t Threaded) (directives map[string]*yaml.Node, pp ParsedProject, err error)
}

// WalkSeams is the set of host-supplied callbacks the whole-project WALK needs for everything
// registry-coupled or host-coupled — the host builds this value and hands it to the registered
// ProjectWalker; the walk mechanism (sdk/loaderkit.Walk) calls each seam and never does the
// coupled work itself (boundary law clause D: the walk consults host-threaded DATA/mechanisms,
// never the provider registry directly).
type WalkSeams struct {
	// Parser is the per-document parse (the host passes the registered DocParser).
	Parser DocParser
	// Boundary runs at each PROJECT boundary (the root file AND each namespace root) BEFORE that
	// boundary's documents parse: the host does the parse pre-scan + connect-declared-kind-plugins
	// side effects (registry mutation). data = the boundary file bytes.
	Boundary func(dir string, data []byte) error
	// Threaded returns the current registry-derived kind-recognition snapshot. Called fresh per
	// document parse (the host's loaderThreaded()).
	Threaded func() Threaded
	// ResolveRef resolves an import ref (local path OR remote "@host/org/repo[/sub]:ver") to a
	// stable cache KEY + a concrete on-disk PATH. The host owns remote fetch + cache + auto-migration.
	ResolveRef func(ref, baseDir string) (key, path string, err error)
	// GateDoc runs the host #NodeDoc CUE validate-before-execute gate on one raw document's bytes.
	GateDoc func(label string, raw []byte) error
	// RepoIdentity returns the canonical repo identity of an import ref (for the cycle-break), or ""
	// (the host's nsRepoIdentity). Empty → version-keyed fallback.
	RepoIdentity func(ref, baseDir string) string
}

// ProjectWalker is the swappable WHOLE-PROJECT WALK seam: the loader plugin candy implements it
// (candy/plugin-loader, delegating to loaderkit.Walk), and the host resolves the registered loader
// provider to it and calls it once per project load — so an alternative loader plugin serves a
// different walk mechanism by implementing this. Typed (no wire envelope) since the compiled-in
// placement passes live Go callbacks (WalkSeams) that cannot cross a JSON envelope. rootData is the
// (possibly bootstrap-transformed) root document bytes; rootIdentity seeds the namespace
// cycle-break with the root project's own repo identity.
type ProjectWalker interface {
	WalkProject(rootDir string, rootData []byte, rootIdentity string, seams WalkSeams) (LoadedProject, error)
}

// MaterializedProject accumulates the kind-decoded ENTITY maps ONE document's or ONE discovered
// node's fold produces — the SAME fields charly-core's *UnifiedFile carries for this purpose
// (Box/Candy/Fleet/PluginKinds); the host copies them in before a Materializer call and back out
// after (cheap map-header copies — maps are reference types, so this is NOT a deep copy).
//
// The 5 standalone-substrate-TEMPLATE kinds (vm/pod/kubernetes/local/android) do NOT get their own
// dedicated fields here — they fold into PluginKinds[disc][name] like every other templated kind
// (distro/builder/init/sidecar/resource/agent already do), so foldStandaloneTemplateReply
// (charly/node_normalize.go) needs NO per-kind-word switch to pick a destination field: the
// generic write `acc.PluginKinds[disc][name] = replyJSON` IS the fold, for any disc. charly-core's
// UnifiedFile.VM()/.Pod()/.Kubernetes()/.Local()/.Android() are now DERIVED accessor methods reading
// PluginKinds, mirroring the established Distros()/Builders()/Inits() pattern — not stored fields
// — so there is nothing left to copy for them either.
//
// SDD classification (hand-written, non-wire — precedent: ParsedProject/LoadedProject/CandyRefs/
// ScannedCandy above, the established sibling family this type extends): same-process PIPELINE
// STATE crossing ONLY the compiled-in typed Materializer seam below — never marshaled, because the
// loader plugin is bootstrap-critical and ALWAYS compiled-in (see the package doc above). A live
// `cue exp gengotypes` spike on this exact shape (all fields already-portable
// map[string]json.RawMessage / map[string]FleetNode / map[string]map[string]json.RawMessage —
// zero disjunctions, zero open tails) would generate a faithful plain struct per the CAN/CANNOT
// quick reference in /charly-internals:go — CUE-sourcing it is NOT precluded by shape. It stays
// hand-written here instead because it is not a wire type at all: it never crosses a real
// marshal/unmarshal boundary (the compiled-in placement passes it as a live *MaterializedProject
// Go pointer, exactly like WalkSeams' live callbacks above) — the SAME reasoning CandyRefs/
// ScannedCandy already document for this file, which the SDD "wire types are CUE-sourced without
// exception" mandate does not reach (it binds host↔plugin / render-context WIRE carriers; this is
// same-process pipeline state, the class loader_seam.go's own precedent already carves out).
type MaterializedProject struct {
	Box         map[string]json.RawMessage
	Candy       map[string]json.RawMessage
	Fleet       map[string]FleetNode
	PluginKinds map[string]map[string]json.RawMessage
}

// MaterializeSeams is the set of host-supplied callbacks the per-node kind-decode DISPATCH needs
// for everything registry-coupled — resolving a node's discriminator word to its live Provider and
// invoking it stays host-side (boundary law clause M: provider_registry.go + provider_kind_invoke.go
// are the TRUE mechanism, same bucket as WalkSeams.Boundary/ResolveRef/GateDoc above); the
// Materializer NEVER touches the provider registry directly.
type MaterializeSeams struct {
	// DecodeEntity resolves pn's discriminator against the provider registry and, if a provider is
	// found, folds the decoded entity into acc (mutating whichever field the kind belongs in) — the
	// SAME dispatch the former in-core normalizeNodeInto/runPluginKind always performed. found=false
	// (no error) means the registry has no provider for pn.Disc — the Materializer applies its OWN
	// not-found policy from there, using Threaded + the callbacks below.
	DecodeEntity func(pn ParsedNode, acc *MaterializedProject) (found bool, err error)
	// BuildFleetEntity folds pn as a deploy-substrate entity into acc.Fleet — the fallback for a
	// RECOGNIZED (Threaded.DeploySubstrates) but not-yet-connected external deploy substrate word.
	BuildFleetEntity func(pn ParsedNode, acc *MaterializedProject) error
	// InKindConnectPass reports whether the loader is inside the re-entrant connect-declared-kind
	// pre-pass (a nested load triggered by connecting a plugin) — a still-unconnected declared kind
	// is silently deferred (skip, no error) during this pass.
	InKindConnectPass func() bool
	// DeclaredKindConnectError returns the retained build/connect failure for a declared-but-
	// unconnected kind word, or nil if it simply hasn't been reached yet.
	DeclaredKindConnectError func(word string) error
}

// Materializer is the swappable per-node kind-decode DISPATCH seam (#46 unit 1, K1): the loader
// plugin candy implements it (candy/plugin-loader, delegating to loaderkit.Materialize), and the
// host resolves the registered loader provider to it and calls it once per parsed entity node — so
// an alternative loader plugin applies a different not-found/fallback policy by implementing this.
// Typed (no wire envelope), mirroring DocParser/ProjectWalker/CandyScanner above.
type Materializer interface {
	MaterializeNode(pn ParsedNode, t Threaded, seams MaterializeSeams, acc *MaterializedProject) error
}

// CandyScanner is the swappable CANDY-SCAN seam (W9): the loader plugin candy implements it
// (candy/plugin-loader, delegating to loaderkit.ScanCandyManifest), and the host resolves the
// registered loader provider to it and calls it once per candy directory. Typed (no wire envelope)
// — the compiled-in placement passes a live parseManifest callback (a Go function value, exactly
// like WalkSeams.Parser above) since the candy-manifest parse itself is registry-coupled (it
// threads the registered DocParser + the registry-derived Threaded snapshot) and so stays a
// HOST-injected seam rather than moving into loaderkit — only the SCAN+CONSTRUCT logic (fs-probes,
// the bake_plugin/package-derivation/port-normalization business logic) moves. Returns the two
// resolved envelope views (spec.CandyModel + spec.CandyView) DIRECTLY — the same shape
// sdk/deploykit.NewSpecCandyModel already consumes to build a spec.CandyReader, so core never
// needs a concrete Candy struct to hold the scan result.
//
// ScanCandyManifest is named distinctly from the ESTABLISHED exported charly.ScanCandy(dir) (the
// whole-project scan-all-candies entry point, charly/layers.go) — a similar name on a
// single-candy-directory scan risks confusion once both exist side by side during the cutover.
type CandyScanner interface {
	// ParseCandyManifest is the candy-MANIFEST parse the two scan methods below take as their
	// `parseManifest` seam. It relocated OUT of charly core in K-wave 2 cone R1 (A2 unit 2) into
	// sdk/loaderkit, so a plugin driving its own scan (candy/plugin-build's remote-repo fetch) can
	// parse manifests ITSELF instead of round-tripping to the host for every candy directory.
	//
	// It reaches core through this seam because charly/ may not import sdk/loaderkit (import
	// purity); core's parseCandyYAML is the thin wrapper that supplies the two values the mechanism
	// needs from the host — t, the registry-derived kind-recognition snapshot, and vocab, the build
	// vocabulary the misplaced-section shape guard consults. The clause-B buildCandy factory is NOT
	// on this path: an RDD spike over the whole 324-manifest corpus proved the pre-move
	// pn->genericNode->pn round trip through it was an identity (321 node-form manifests plus all 3
	// error paths, byte-identical).
	ParseCandyManifest(path string, t Threaded, vocab CandyVocab) (*Candy, error)
	// ProjectCandiesScanned scans or synthesizes a candy per uf.Candy entry off an ALREADY-LOADED
	// project — the local candy scan's body, relocated to sdk/loaderkit in K-wave 2 cone R1 (A2 unit
	// 3) so a plugin holding a *UnifiedFile no longer round-trips to the host to turn it into a
	// ScannedCandy map. Reaches core through this seam for the same import-purity reason
	// ParseCandyManifest does.
	ProjectCandiesScanned(uf *UnifiedFile, rootDir string, parseDoc func(path string) (*Candy, error)) (map[string]ScannedCandy, error)
	ScanCandyManifest(path, name, manifestName string, parseManifest func(path string) (*Candy, error)) (CandyModel, CandyView, CandyRefs, error)
	// ScanInlineCandy builds the two views for a candy declared INLINE in a unified charly.yml —
	// ly is already the parsed body (no manifest file, no parseManifest seam needed). sourceDir is
	// the charly.yml's own directory.
	ScanInlineCandy(name, sourceDir string, ly *Candy) (CandyModel, CandyView, CandyRefs)
	// ScanRemoteCandy scans specific candies out of a downloaded remote repository directory —
	// only the bare refs in wantRefs (each "github.com/org/repo/candy/x" form). Sets each result's
	// CandyView.Remote/.RepoPath/.SubPathPrefix and runs the remote-sibling-dep qualification
	// (QualifyRemoteSiblingDeps) before returning, mirroring the pre-move charly/layers.go
	// ScanRemoteCandy, which did the same two things (post-scan.Remote/RepoPath/SubPathPrefix
	// mutation, then qualifyRemoteSiblingDeps) on the live *Candy it had just built.
	ScanRemoteCandy(repoDir, repoPath string, wantRefs map[string]bool, parseManifest func(path string) (*Candy, error)) (map[string]ScannedCandy, error)
}

// LoaderExecutor is the typed host-leg contract for the registry-/host-coupled loader steps the
// kind-blind LoadUnified orchestration cannot do itself: the bootstrap-phase plugin invocation, the
// registry-coupled import/discover walk, the materialize kind-decode + merge, and the two
// registry-resolving validators. Promoted here from sdk/loaderkit (#55 loader-keystone) so charly
// core can hold the host LoaderExecutor implementation while importing ONLY the dedicated spec
// module — the interface's method signatures already reference only spec types (Threaded /
// LoadedProject / UnifiedFile), so promoting it is a relocation, not an invention. Because the
// methods are TYPED, a compiled-in placement pays no envelope tax; only a true out-of-module plugin
// marshals (the existing LoadedProject / UnifiedFile envelopes). loaderkit.LoadSeamsFromExecutor
// consumes this to build its internal LoadSeams.
type LoaderExecutor interface {
	// LoaderThreaded returns the CURRENT registry-derived snapshot (recognized kinds / deploy
	// substrates / DeployTraits / ExternalDeploySubstrates / …). Called FRESH at each DATA-seam
	// invocation — NEVER cached at seam-build time — because the walk's connect-declared-kind pass
	// mutates the registry BETWEEN seam construction and the post-walk validators.
	LoaderThreaded() Threaded
	// RunBootstrapPhase invokes every registered bootstrap-phase plugin on the raw root bytes,
	// returning the (possibly transformed) bytes. A leg failure (e.g. a broken reverse-channel
	// HostBuild round trip) is a hard error — never a silent no-op fallback to the raw bytes,
	// which would let LoadUnified proceed on an un-bootstrapped root with zero visible signal.
	RunBootstrapPhase(data []byte) ([]byte, error)
	// WalkProject runs the kind-blind import/discover/namespace walk (the registered ProjectWalker,
	// reached via the host's WalkSeams) → the generic LoadedProject envelope. The host #NodeDoc CUE
	// gate (WalkSeams.GateDoc) runs INSIDE this walk.
	WalkProject(dir string, rootData []byte) (LoadedProject, error)
	// MaterializeLoadedProject replays the host's per-document/per-namespace MATERIALIZE + root-wins
	// MERGE over the walk envelope (registry kind-decode via the registered Materializer).
	MaterializeLoadedProject(lp *LoadedProject, merged *UnifiedFile, byID map[int64]*UnifiedFile) error
	// ValidateAndroidDevices enforces the kind:android box⊻adb XOR — resolves android templates via
	// the provider registry (host-coupled), so a leg, not a pure loaderkit move.
	ValidateAndroidDevices(uf *UnifiedFile) error
	// ValidatePreemptible validates preemptible / requires_exclusive / requires_shared across the
	// deploy map, including the resource-vocabulary cross-check (resolves the resource plugin kind +
	// vm/resource entities via the registry) — host-coupled, so a leg.
	ValidatePreemptible(uf *UnifiedFile) error
}

// ProjectLoader is the swappable whole-project LOAD-ENTRY seam (#55 loader-keystone) — the terminal
// loader endpoint every command reaches to load a project's charly.yml. The loader plugin candy
// implements it (candy/plugin-loader, delegating to loaderkit.LoadUnified over
// loaderkit.LoadSeamsFromExecutor), and the host resolves the registered loader provider to it and
// drives LoadUnified THROUGH it — so charly core imports ONLY the dedicated spec module, never the
// loaderkit mechanism, to load its own config. The host supplies the registry-/host-coupled legs as
// a LoaderExecutor; the plugin owns the kind-blind orchestration. Typed (no wire envelope), the
// LOAD-ENTRY sibling of DocParser/ProjectWalker/CandyScanner/Materializer above — the compiled-in
// placement (the loader MUST always resolve; it is the config front-end) calls it directly, resolved
// at init() before the first load so there is no bootstrap cycle.
type ProjectLoader interface {
	LoadUnified(dir string, exec LoaderExecutor) (*UnifiedFile, bool, error)
	// DecodeEntityViaCUE is the kind-blind per-entity CUE decode mechanism (shorthand normalize +
	// CUE-ingest + Decode), relocated to loaderkit (K1 unit 1): normalizes node against t's shape
	// (shorthand expansion, scalar→string coercion), then CUE-decodes the result into out. node must
	// BE the entity value (a candy body / a single kind entity / an assembled node-form body), not a
	// kind-keyed wrapper; does not mutate the input node. Compiled-in only (the loader is
	// bootstrap-critical), no wire envelope — every kind/candy/node-form decode in charly core
	// routes through this seam instead of importing loaderkit directly.
	DecodeEntityViaCUE(node *yaml.Node, t reflect.Type, out any, label string) error
	// ValidateEntityClosedCUE unifies a single entity with #<Kind> and validates
	// it WITHOUT requiring concreteness — closedness violations (unknown keys) and type/enum/regex
	// conflicts, but not missing-required fields (K1 unit 2 relocation).
	ValidateEntityClosedCUE(kind, label string, entity cue.Value) error
	// ValidateEntityCUE is the CONCRETE twin of the above — closedness PLUS missing-required
	// fields and unresolved disjunctions. The schema-tightening corpus (charly's
	// cue_tighten_test.go) drives it as the regression guard that the modeled subtrees stay
	// strict; it needs the host's registry-derived verb primaries to parse its candy cases, so it
	// runs host-side and reaches the loader's compiled schema through this seam.
	ValidateEntityCUE(kind, label string, entity cue.Value) error
	// CueDocFromYAML ingests one YAML document into a cue.Value (the whole doc), built with the
	// loader's own cue.Context so the result can Unify against its compiled schema (K1 unit 2
	// relocation; the host-threaded schema handle was dropped in K-wave 2 cone R1).
	CueDocFromYAML(path string, data []byte) (cue.Value, error)
	// ValidateNodeDocCUE validates a unified node-form document (raw YAML bytes) by unifying EACH
	// top-level entity node against #Node — the load-time "validate-before-execute" structural gate
	// (K1 unit 2 relocation).
	ValidateNodeDocCUE(label string, data []byte) error
	// ApplyCueDefaults fills schema-declared defaults into an already-RESOLVED entity by unifying
	// its marshaled form with #<Kind> and decoding back (K1 unit 2 relocation).
	ApplyCueDefaults(kind string, out any) error
	// ResolveMergedDeployTree returns the top-level Fleet (deploy-node) map — the merged project
	// charly.yml + per-host operator overlay, ready for dotted-path traversal — the host-side
	// merged-tree read the check host seams (deployNodePluginContext + check_venue_resolve) need.
	// It is the merged-tree sibling of LoadUnified: LoadUnified returns the PROJECT-only tree
	// (loadmodel.go Fleet has no overlay field), so a caller that needs the per-host operator
	// overlay merged in routes through THIS seam instead. The merge LOGIC (the loaderkit
	// project+overlay projection+merge) stays in the ONE copy in sdk/loaderkit
	// (loaderkit.ResolveMergedTreeViaExecutor); the host reaches it through this compiled-in seam
	// instead of importing loaderkit directly (#55 coneA Q2(1) — charly core's check_cmd.go sheds
	// its loaderkit import). The in-proc executor is threaded on ctx via sdk.ContextWithExecutor
	// (the SAME in-proc reverse-channel path ExecutorForInvoke uses for Invoke) so the seam
	// signature stays spec-typed — the plugin-side impl retrieves it via sdk.ExecutorFromContext.
	// Compiled-in only (the loader is bootstrap-critical), no wire envelope.
	ResolveMergedDeployTree(ctx context.Context, dir string) (map[string]FleetNode, error)
	// MaterializeLoadedProject replays the whole-project per-document/per-namespace MATERIALIZE +
	// root-wins MERGE over a walk envelope, driving loaderkit's kind-blind orchestration with the
	// host-supplied per-node seams — so charly core reaches materialize WITHOUT importing loaderkit
	// (#55 2b C3). The host's own LoadUnified path AND the loader-materialize HostBuild seam (serving a
	// plugin-side loader) both route through here.
	MaterializeLoadedProject(lp *LoadedProject, merged *UnifiedFile, byID map[int64]*UnifiedFile, seams MaterializeProjectSeams) error
	// MarshalMaterialized marshals a materialized UnifiedFile into the wire envelope the
	// loader-materialize HostBuild seam returns to a plugin-side loader (it captures the nested
	// plugin-kind maps loaderkit-internally, so the host reaches it through this seam).
	MarshalMaterialized(uf *UnifiedFile) ([]byte, error)
	// ValidateAndroidDevices enforces the kind:android box⊻adb XOR; the host supplies the
	// registry-resolve callback (the validation LOGIC stays in loaderkit).
	ValidateAndroidDevices(uf *UnifiedFile, resolveAndroid func(json.RawMessage) (*ResolvedAndroid, error)) error
	// ValidatePreemptible validates preemptible / requires_exclusive / requires_shared across the
	// deploy map; the host supplies the registry-resolve callbacks (the validation LOGIC stays in
	// loaderkit).
	ValidatePreemptible(uf *UnifiedFile, resolveResource func(json.RawMessage) (*ResolvedResource, error), resolveVm func(json.RawMessage) (*ResolvedVm, error)) error
	// ScanCandyFromLocal runs the candy-scan fetch fix-point (remote-ref collect, fetch, per-entity
	// version arbitration, host-completion + finalize) over a local candy set, driving the host-coupled
	// legs through the caller-supplied ScanSeams closures — so charly core reaches the scan MECHANISM
	// through this compiled-in seam instead of importing loaderkit (#55 C3b-ii). The scan LOGIC stays in
	// the ONE copy in sdk/loaderkit (candy/plugin-build reaches it directly, being a plugin).
	ScanCandyFromLocal(localScanned map[string]ScannedCandy, initCfg *InitConfig, seams ScanSeams) (map[string]CandyReader, error)
	// RunDiscover walks the flat generic discover: scan-spec list, parsing each discovered manifest via
	// the host-supplied WalkSeams — the discover half of the loader mechanism, reached via the seam so
	// charly core never imports loaderkit for it.
	RunDiscover(rootDir string, specs []ScanSpec, seams WalkSeams) ([]DiscoveredManifest, error)
	// FinalizeScannedCandies is the scan pipeline's finalize choke point (host-completion + bare-string
	// the refs + wrap into the FINAL CandyReader). It is deploykit-coupled and stays in loaderkit; the
	// host reaches it through this seam — the dominant shared choke point across charly's scan call sites.
	FinalizeScannedCandies(scanned map[string]ScannedCandy, initCfg *InitConfig) map[string]CandyReader

	// -- K1 unit 3a: fleet/resource-member kind-decode SUPPORT helpers (node_fleet.go/
	// node_normalize.go) — pure functions of a discriminator word + the registry-derived Threaded
	// snapshot (never a live registry query), consumed by the TRUE clause-M dispatch
	// (provider_kind_invoke.go) and its BuildFleetEntity fallback. DATA-driven via t.DeploySubstrates
	// / t.DeployTraits (the SAME snapshot loaderThreaded() already fills for the parse), never a
	// kind-word switch.

	// IsResourceDisc reports whether a discriminator names a deploy-substrate kind (the markers of a
	// fleet member / fleet-shaped node) — the CUE-derived #ResourceKind vocab, OR a recognized
	// external deploy substrate word (t.DeploySubstrates).
	IsResourceDisc(d string, t Threaded) bool
	// FleetTargetForDisc maps a node discriminator to the FleetNode Target — DATA-driven via
	// t.DeployTraits: a word with no declared deploy traits is TARGETLESS (e.g. group).
	FleetTargetForDisc(d string, t Threaded) string
	// SetFleetCrossRef sets the deploy's cross-ref from a scalar discriminator value — DATA-driven
	// via t.DeployTraits' ImageBacked trait (image-backed → dn.Image; otherwise → dn.From). A
	// targetless word (no declared traits) sets neither.
	SetFleetCrossRef(dn *FleetNode, disc, ref string, t Threaded)
	// IsStandaloneResourceKind reports whether disc names one of the substrate kinds that are BOTH a
	// standalone TEMPLATE and a deploy — DATA-driven via t.DeployTraits (same fact
	// FleetTargetForDisc/SetFleetCrossRef resolve against).
	IsStandaloneResourceKind(disc string, t Threaded) bool
	// FoldStandaloneTemplateReply folds a standalone-template kind's echoed reply JSON into acc's
	// generic PluginKinds[disc][name] map — the C2-substrate TEMPLATE fold arm, GENERIC by
	// construction (no per-kind-word switch).
	FoldStandaloneTemplateReply(disc, name string, replyJSON json.RawMessage, acc *MaterializedProject) error

	// -- K1 unit 3b: the entity-body assembly + fleet/resource-member tree-builder mechanism
	// (node_build.go/node_fleet.go/node_normalize.go) — operates on ParsedNode (the wire-safe
	// parsed-entity shape), never *genericNode (charly core's host-internal reconstruction, which
	// stays core solely for the TRUE clause-M dispatch's bootstrap-critical candy/box routing).

	// AssembleEntityBody returns the DOCUMENT-wrapped entity-body mapping to decode: pn's body
	// value (an empty mapping when the value is null/absent or a scalar cross-ref).
	AssembleEntityBody(pn ParsedNode) (*yaml.Node, error)
	// DecodeNodeValue decodes pn's body via the shared CUE entity decoder into out (a *struct) —
	// the SAME entity-body assembler + CUE decode every candy/kind/node-form decode goes through.
	DecodeNodeValue(pn ParsedNode, out any) error
	// EntityBodyJSON returns a node's kind-value mapping as canonical JSON, generically — with NO
	// concrete-kind Go type.
	EntityBodyJSON(pn ParsedNode) (json.RawMessage, error)
	// BuildFleetNode recursively builds a FleetNode from a fleet/resource node.
	BuildFleetNode(pn ParsedNode, t Threaded) (*FleetNode, error)
	// BuildResourceMemberChildren decodes pn's RESOURCE-MEMBER entity children into a
	// name→*FleetNode map via the SAME BuildFleetNode recursion — the SINGLE source of truth for
	// authored member-tree decode.
	BuildResourceMemberChildren(pn ParsedNode, t Threaded) (map[string]*FleetNode, error)
	// BuildFleetNodeInto builds pn into a FleetNode and registers it in acc's Fleet map — the
	// fallback for a recognized-but-not-yet-connected external deploy substrate word
	// (MaterializeSeams.BuildFleetEntity's implementation).
	BuildFleetNodeInto(pn ParsedNode, t Threaded, acc *MaterializedProject) error
	// IsDeployShape reports whether a substrate node is a DEPLOY (vs a standalone template).
	IsDeployShape(pn ParsedNode) bool
	// DecodeStandaloneTemplateJSON canonicalizes pn (a substrate TEMPLATE node) to the JSON the
	// host threads to the substrate plugin, GENERICALLY — with NO concrete-kind Go type.
	DecodeStandaloneTemplateJSON(pn ParsedNode, t Threaded) (json.RawMessage, error)
	// ResourceChildren returns pn's children whose discriminator is itself a resource/fleet kind
	// (the CUE-derived #ResourceKind vocab).
	ResourceChildren(pn ParsedNode) []ParsedNode

	// -- K1 unit 3c: the box-validate entity-tree walk (completes the K1 unit 2 deferral) — the
	// `charly box validate` candy-manifest entry point + its node-form step-typo walk. t/parser are
	// host-supplied (the registry-derived Threaded snapshot + the resolved DocParser); neither
	// method queries the registry itself.

	// ValidateCandyManifestCUE validates a candy manifest: the whole-document #NodeDoc structural
	// gate, then the parsed+desugared entity-tree walk (ValidateNodeFormSteps).
	ValidateCandyManifestCUE(path string, data []byte, t Threaded, parser DocParser) error
	// ValidateNodeFormSteps parses a node-form document and validates EVERY entity's (and nested
	// sub-entity's) assembled body against its closed per-kind def — the step-typo gate for
	// candies, boxes, pods, deploys, and check beds alike.
	ValidateNodeFormSteps(path string, data []byte, t Threaded, parser DocParser) error

	// -- K1 unit 4 / K-wave 2 cone R1: the remote-repo fetch ORCHESTRATION + candy-ref collection
	// mechanism — EnsureRepoDownloaded (local-override short-circuit, cache-hit check, cache-miss
	// dispatch, post-fetch schema auto-migration) and CollectRemoteRefsOpts (the base/builder/
	// candy-ref graph walk).
	//
	// These took a host-built RefsCollectSeams until cone R1. They now take ctx and the loader plugin
	// builds those legs ITSELF over the executor the host threads on (sdk.ExecutorFromContext — the
	// SAME in-proc reverse channel ResolveMergedDeployTree uses). charly core was not defining a
	// mechanism by assembling them; it was only resolving three peers and reading one env var on the
	// loader's behalf, which the defines-vs-calls test classifies as an R-item. So charly/refs.go and
	// charly/refs_threaded.go are gone and every caller reaches this seam directly.

	// EnsureRepoDownloaded downloads repoPath@version if not already cached and returns the cache
	// path, auto-migrating it to the latest schema CalVer.
	EnsureRepoDownloaded(ctx context.Context, repoPath, version string) (string, error)
	// ResolveProjectRepo turns a --repo spec ("owner/repo", "owner/repo@ref", a host-qualified
	// path, or the "default" literal) into a local cache path a caller can chdir into. It is the
	// SAME clone-and-cache machinery EnsureRepoDownloaded drives, with the spec normalization and
	// default-branch resolution in front — pure spec vocabulary over one fetch, which is why it
	// belongs beside the fetch rather than in the kernel (K-wave 2 cone R1 relocated it out of
	// charly/main_repo.go).
	ResolveProjectRepo(ctx context.Context, repoSpec string) (string, error)
	// CanonicalRef resolves one `import:` ref — a local path, or
	// "@host/org/repo[/sub/path]:version" — relative to baseDir, into its stable dedup key and its
	// on-disk path, fetching a remote ref through the same clone-and-cache machinery. It is the
	// mechanism behind WalkSeams.ResolveRef; the host wires that seam to this method rather than
	// holding the resolution itself (K-wave 2 cone R1 relocated it out of charly/unified.go, the
	// same route ResolveProjectRepo took).
	CanonicalRef(ctx context.Context, ref, baseDir string) (key, path string, err error)
	// CollectRemoteRefsOpts collects all unique remote refs reachable from cfg's build/deploy
	// targets + layers' manifest depends/candy fields, grouped by (repoPath, version).
	CollectRemoteRefsOpts(ctx context.Context, cfg *Config, layers map[string]CandyReader, opts ResolveOpts) ([]RemoteDownload, error)
}

// RefsCollectSeams is the set of host-supplied callbacks EnsureRepoDownloaded/
// CollectRemoteRefsOpts need for everything registry-coupled — the host builds this value and
// hands it to the ProjectLoader seam call; the mechanism never touches the provider registry
// directly (boundary law clause M: the resolve+invoke dispatch stays host-side, reached through
// these callbacks, exactly like WalkSeams/MaterializeSeams above).
//
// K-wave 2 cone R1: the value is no longer built by charly core. candy/plugin-loader assembles it
// per call from the ctx-threaded executor (refs_seams.go) — core was only resolving three peers and
// reading one env var on the loader's behalf, which is a CALL, not a mechanism. The struct itself
// stays: it is still how the kind-blind loaderkit mechanism receives its registry-coupled legs, and
// keeping it a plain parameter is what lets the mechanism be unit-tested with stubs.
type RefsCollectSeams struct {
	// Downloader is the registered remote-repo fetch backend (P7). The loader plugin reaches it as a
	// peer — InvokeProvider(class:"refs", word:"refs", OpResolve) — and wraps that dispatch in this
	// interface; a cache-miss download goes through it.
	Downloader RefsDownloader
	// MigrateCache brings a remote-repo cache's PROJECT files up to the head schema via the
	// compiled-in command:migrate plugin — registry-coupled (resolves ClassCommand "migrate" +
	// Invoke), so it stays a host-supplied callback.
	MigrateCache func(path string) error
	// ResolveLocal projects one opaque `kind:local` template body into a *ResolvedLocal via
	// candy/plugin-substrate's OpResolve leg — registry-coupled (Invoke), so it stays a
	// host-supplied callback.
	ResolveLocal func(body json.RawMessage) (*ResolvedLocal, error)
	// OverrideEnvValue is the raw CHARLY_REPO_OVERRIDE env value (RDD local-overrides) — the host
	// reads os.Getenv once; this mechanism never touches the env var NAME (spec/proc.RepoOverrideEnv,
	// #55 W3 B2-full — shared by charly core's plugin_loader.go and candy/plugin-check's
	// bed_session.go, both of which read/write it independently).
	OverrideEnvValue string
}

// RefsDownloader is the swappable remote-repo FETCH BACKEND seam (P7): the host dispatches every
// cache-miss download through a RefsDownloader; the DEFAULT (candy/plugin-refs, delegating to
// kit.DownloadRepo) fetches via git, and an alternative refs plugin can serve a different backend
// (OCI/S3-hosted candies) by registering a different RefsDownloader. Mirrors DocParser/ProjectWalker/
// CandyScanner above: a typed interface a compiled-in plugin implements alongside its provider, so
// the host calls it in-proc with no wire envelope. Relocated from sdk/kit (FLOOR-SLIM axis-A
// mechanical batch) — the interface contract itself is kind-blind and registry-decoupled; the
// default git-fetch backend (kit.DefaultDownloader) stays in kit, wrapping the spec/refs.DownloadRepo
// git primitive (relocated to the spec/refs fabric slice, re-exported by kit for existing callers).
type RefsDownloader interface {
	// Download fetches repoPath@version into the local repo cache and returns the cache path.
	// Called only on a cache MISS (the host checks IsRepoCached first).
	Download(repoPath, version string) (string, error)
}

// CandyRefs carries the RICH require:/candy:/bake_plugin: refs (CandyRefEntry, with a mutable
// .Resolved) a freshly scanned candy declares.
//
// SDD classification (hand-written, non-wire — precedent: ParsedProject/LoadedProject above, the
// original hand-written-contract types this seam file establishes): CandyRefs is same-process
// PIPELINE STATE crossing ONLY the compiled-in typed CandyScanner seam — it is never marshaled,
// because the loader plugin is bootstrap-critical and ALWAYS compiled-in (see the package doc
// above: "the loader must ALWAYS resolve... registered at init() before the first load"), so this
// seam never crosses a real wire the way an out-of-process plugin's gRPC envelope would. It exists
// only between ScanCandyManifest and the host's qualifyRemoteSiblingDeps (which sets .Resolved on a
// remote candy's plain-name sibling deps) and the FINAL bare-string conversion into
// CandyView.Require/.IncludedCandy (mirrors the pre-move projectCandyView's bareRefs() call, which
// ran AFTER qualification on the live *Candy — this type is what lets that same ordering survive
// the *Candy struct's departure). The FINAL bare-string form lands on CandyView.Require/
// .IncludedCandy and CandyModel.BakePlugin (FinalizeCandyRefs, sdk/loaderkit).
type CandyRefs struct {
	Require       []CandyRefEntry
	IncludedCandy []CandyRefEntry
	BakePlugin    []CandyRefEntry
}

// ScannedCandy gathers one candy's full scan result — the two resolved envelope views plus the
// rich pre-qualification refs.
//
// SDD classification: same non-wire, same-process pipeline-state rationale as CandyRefs above (one
// note covers both) — it is the mutable intermediate the whole scan→fetch→qualify→arbitrate
// pipeline (charly's ScanAllCandy family) carries in place of the pre-move *Candy, until the FINAL
// step bare-strings the refs (FinalizeCandyRefs) and wraps (Model, View) into a spec.CandyReader
// via sdk/deploykit.NewSpecCandyModel.
type ScannedCandy struct {
	Model CandyModel
	View  CandyView
	Refs  CandyRefs
}
