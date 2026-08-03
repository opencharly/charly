package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/opencharly/spec/refs"
	"github.com/opencharly/spec/spec"
)

// -----------------------------------------------------------------------------
// Unified YAML Format — Parts B/C/D/E of the refactor plan.
//
// `charly.yml` is the ONE filename and the only file a project needs: the entry
// point (import: + discover:) plus the inline kinds (vm/pod/k8s/check/local/
// android/deploy + any build-vocabulary overrides). Boxes and candies are
// DISCOVERED per name as box/<name>/charly.yml and candy/<name>/charly.yml. The
// default distro/builder/init/resource build vocabulary AND sidecar templates
// are embedded in the binary (charly/charly.yml, //go:embed — unified
// node-form, parsed by the SAME loader as any project charly.yml); a project
// declares distro:/builder:/init:/resource:/sidecar: only to extend or override
// it. Legacy per-kind files (box.yml/vm.yml/...) still LOAD as flat `import:`
// items, never the canonical layout.
//
// Key properties:
//   - name-first node-form documents (`<name>: {<kind>: …}`), routed by SHAPE —
//     a legacy kind-keyed / root-shape document is hard-rejected at kit.ClassifyDoc
//     with a `charly migrate` hint — never by filename;
//   - import: for composition — a flat root-merge string OR a namespaced child import;
//   - discover: for recursive directory scan of node-form standalone files;
//   - every file is read as a multi-document YAML stream so concatenated
//     (`---` separated) node-form documents work naturally.
// -----------------------------------------------------------------------------

// UnifiedFileName is the canonical root file of the unified format. The value
// lives in kit (the importable host-engine shared with out-of-tree plugin candies);
// this is the in-core alias so every core call site is unchanged.
const UnifiedFileName = spec.UnifiedFileName

// The on-disk charly.yml schema version is a CalVer string (e.g.
// 2026.141.1530) — the same scheme as image tags. LatestSchemaVersion()
// (CUE-owned via spec.SchemaVersion) is the HEAD value; the LoadUnified gate
// refuses anything older with a hint pointing at `charly migrate`.

// UnifiedFile (K1 keystone, task #24 unit 1) relocated to sdk/loaderkit — see
// spec.UnifiedFile. Every field type was already sdk-portable (spec.*/deploykit.*/plain
// maps; Box was already spec.BoxMap via the (now-deleted) boxMap alias, Candy already a plain
// map[string]json.RawMessage via the (now-deleted) candyMap alias), so the type carried NO
// charly-core dependency of its own. Every charly/*.go reference is now spec.UnifiedFile.

// ImportEntry, ImportList, DiscoverConfig, and ScanSpec are the kind-blind
// config-loader document DIRECTIVE types — relocated to sdk/kit
// (loader_directives.go) so charly core AND sdk/loaderkit share ONE copy (R3).
// See spec.ImportEntry / spec.ImportList / spec.DiscoverConfig / spec.ScanSpec.

// InlineCandy (K1 keystone, task #24 unit 1) relocated to sdk/loaderkit — see
// spec.InlineCandy.

// DeploymentsSection (the legacy v3 plural `deployments:` wrapper type) was
// DELETED as dead code (radical dead-code removal): after the field-singular
// cutover (2026-05) spec.UnifiedFile.Bundle is a flat map and Provides moved
// to root level, and the last real referent — migrate_unified.go — is long gone.
// Its only surviving mentions were prose (this file) plus the
// TestLoadUnified_DeploymentsSection name (which
// tests that the legacy `deployments:` YAML key is hard-rejected at load, never
// the Go type). No code constructed or consumed it.

// -----------------------------------------------------------------------------
// Entity kind table — drives scanner + router + merge path.
// -----------------------------------------------------------------------------

// The kind vocabulary for shape classification is the CUE-derived kindWordSet
// (reserved_registry.go); the former hand kindKeys/kindKeysSet lists were deleted
// in the CUE-single-source cutover. Files are generic kind-containers routed by
// shape; there is no per-kind filename — discovery + every per-kind filename are
// configured in charly.yml, never baked into the code.

// -----------------------------------------------------------------------------
// Loader entry point.
// -----------------------------------------------------------------------------

// gateSchemaVersion (K1 keystone, task #24 unit 2) relocated to sdk/loaderkit —
// see loaderkit.GateSchemaVersion (called only from loaderkit.LoadUnified now).
// The retired NormalizeV4Aliases no-op + its two loaderkit-internal call sites were
// deleted as dead code (#55 C3b-ii).

// LoadUnified drives the whole-project load through the registered spec.ProjectLoader SEAM
// (requireProjectLoader) — it NO LONGER calls loaderkit.LoadUnified directly (#55 import-purity
// keystone, the terminal shape). The host passes its own hostLoaderExecutor{} (the typed
// spec.LoaderExecutor reaching each registry-/host-coupled load step by calling the host function
// DIRECTLY — zero marshal, a compiled-in TYPED placement pays no envelope tax); the COMPILED-IN
// candy/plugin-loader implements spec.ProjectLoader and internally runs
// loaderkit.LoadUnified(dir, loaderkit.LoadSeamsFromExecutor(exec)). The seam is registered at init
// (before main), so the host resolves it before loading its own charly.yml — no bootstrap cycle.
// charly core holds only the seam interface (spec) + the host executor legs; the kind-blind
// orchestration (bootstrap phase, schema gates, walk, materialize, venue flatten, member fold,
// descent stamp, the validation chain) lives in loaderkit (sdk), driven by the plugin.
func LoadUnified(dir string) (*spec.UnifiedFile, bool, error) {
	return requireProjectLoader().LoadUnified(dir, hostLoaderExecutor{})
}

// validateDeploymentTree / validateDeployRequiresBox / validateDeploymentChildren /
// validateDeploymentName moved to sdk/spec/deploy_tree_validate.go (FLOOR-SLIM
// K1-proper mechanical batch, zero logic change) — pure structural validation
// over an already-merged map[string]spec.BundleNode with zero registry/host
// coupling, the same D-clause precedent as spec.ClassifyDoc. See
// spec.ValidateDeploymentTree / spec.ValidateDeploymentName.

// canonicalRef resolves an import ref (local path or
// `@host/org/repo[/sub/path]:version`) to a concrete on-disk path AND a stable
// cache key. Remote refs are downloaded into the shared repo cache (and
// auto-migrated). The key dedups identical refs across the whole load so a
// diamond — or the intentional main<->cachyos cycle — of namespaced imports
// resolves exactly once.
func canonicalRef(ref, baseDir string) (key, path string, err error) {
	if strings.HasPrefix(ref, "@") {
		parsed := spec.ParseRemoteRef(ref)
		version := parsed.Version
		if version == "" {
			branch, e := refs.GitDefaultBranch(refs.RepoGitURL(parsed.RepoPath))
			if e != nil {
				return "", "", fmt.Errorf("resolving default branch for %s: %w", parsed.RepoPath, e)
			}
			version = branch
		}
		cachePath, e := EnsureRepoDownloaded(parsed.RepoPath, version)
		if e != nil {
			return "", "", fmt.Errorf("downloading remote ref %q: %w", ref, e)
		}
		return parsed.RepoPath + "@" + version + "/" + parsed.SubPath,
			filepath.Join(cachePath, parsed.SubPath), nil
	}
	p := ref
	if !filepath.IsAbs(p) {
		p = filepath.Join(baseDir, ref)
	}
	abs, e := filepath.Abs(p)
	if e != nil {
		return "", "", fmt.Errorf("resolving %s: %w", ref, e)
	}
	return abs, abs, nil
}

// -----------------------------------------------------------------------------
// Document-shape classifier.
// -----------------------------------------------------------------------------

// docShape (kit.DocShape) + classifyDoc (kit.ClassifyDoc) are the kind-blind
// document-shape classifier — relocated to sdk/kit (loader_classify.go) so
// charly core AND sdk/loaderkit share ONE copy (R3).

// -----------------------------------------------------------------------------
// AI-CLI catalog validation.
// -----------------------------------------------------------------------------

// -----------------------------------------------------------------------------
// Merge helpers.
// -----------------------------------------------------------------------------

// mergeUnified + mergeRawTemplateMap + mergePluginKindsMap + mergeDeployMaps +
// mergeBoxConfig (K1-proper, task #24 follow-up) — the kind-blind document MERGE
// half of the loader. They are pure map/struct merges over an already-parsed
// UnifiedFile with zero charly-core coupling (spec-only), so they live in the
// dedicated spec module (#55 C3b relocated them from sdk/loaderkit/merge.go, the
// same import-purity route MaterializeProjectSeams + MergePluginKindsMap took) —
// so charly core reaches them WITHOUT importing loaderkit. See spec.MergeUnified
// (called from materialize.go) / spec.MergePluginKindsMap (called from
// embed_defaults.go).

// anchorScanSpecs (spec.AnchorScanSpecs) is the discover-path anchoring helper
// — resident in the spec module (load_directives.go, #55 C3b, moved with
// MergeUnified which calls it); sdk/kit keeps a forwarder so charly core AND
// sdk/loaderkit + sdk/kit share ONE copy (R3).

// CheckBeds relocated to sdk/loaderkit (K1 keystone, task #24 unit 1) — see
// spec.UnifiedFile.CheckBeds.

// validateCheckBeds / validateIterateBed relocated to sdk/loaderkit (validate_check_beds.go,
// K1-LOADER RELOCATION LOAD-half) — registry-free kind:check bed validation reading the
// spec.Threaded DATA snapshot (DeployTraits for bed-target classification; DeploySubstrates +
// spec.ResourceKinds for the byte-equivalent isExternalDeploySubstrate) instead of the live
// registry, reached via the LoadSeams.ValidateCheckBeds seam. See loaderkit.ValidateCheckBeds.

// validateAndroidDevices (the kind:android box⊻adb XOR) relocated to
// sdk/loaderkit (validate_capabilities.go) as loaderkit.ValidateAndroidDevices —
// kind-blind clause-R capability logic reaching the registry only via a threaded
// resolve callback (resolveAndroidViaPlugin), the same relocation shape
// ValidateCheckBeds / ValidateEphemeral took. The host wires it through the
// LoaderExecutor.ValidateAndroidDevices leg (load_executor_host.go); a plugin-side
// loader self-serves the SAME validator over InvokeProvider(kind, OpResolve).

// -----------------------------------------------------------------------------
// Discovery scanner (Part D).
// -----------------------------------------------------------------------------

// ApplyDiscover walks every flat scan spec on uf.Discover and registers any
// entity found. Each spec scans its path for directories containing the spec's
// manifest; every discovered manifest is routed by SHAPE. Conflict rule:
// explicit map entries win over discovered entries. scanRoot resolution is
// relative to rootDir (the dir containing charly.yml).
//
// K1 keystone (task #24) unit 3: the WALK+PARSE half (find directories, read +
// classify + gate + parse each manifest's documents) lives in loaderkit.RunDiscover,
// reached via the ProjectLoader seam (requireProjectLoader, #55 C3b-ii) — the SAME
// walker.runDiscover/parseDiscoveredManifest mechanism Walk's own depth-0 discover
// pass already drives internally, reused here rather than duplicated. Only the
// registry-coupled MATERIALIZE fold (foldDiscoveredManifests, materialize.go — shared with
// loaderkit.MaterializeLoadedProject's own discovered-manifest step via the FoldDiscoveredManifests seam, R3) stays host-side.
func ApplyDiscover(uf *spec.UnifiedFile, rootDir string) error {
	dms, err := requireProjectLoader().RunDiscover(rootDir, uf.Discover, hostWalkSeams())
	if err != nil {
		return err
	}
	return foldDiscoveredManifests(dms, uf)
}

// findEntityDirs (kit.FindEntityDirs) + discoverSkipDir (kit.DiscoverSkipDir)
// are the discover-walk PRIMITIVES — relocated to spec/spec
// (loader_discover.go), re-exported by sdk/kit, so charly core AND
// sdk/loaderkit share ONE copy (R3).

// -----------------------------------------------------------------------------
// Projections — extract the existing concrete types from spec.UnifiedFile so the
// existing loaders can become thin wrappers.
// -----------------------------------------------------------------------------

// ProjectConfig/projectConfigCached/ProjectBundleConfig/VM/Pod/K8s/Local/Android/CheckBeds
// relocated to sdk/loaderkit as spec.UnifiedFile methods (K1 keystone, task #24 unit 1) —
// they needed no charly-core-only dependency (Config = spec.Config, an alias, so even
// ProjectConfig's return type was already sdk-native). ResolvePluginKindViaPlugin/
// DecodePluginKindMap moved too, as exported loaderkit package functions (they already took
// their registry-coupled resolve callback as a PARAMETER, so the loop itself was pure).
//
// What STAYS here as free functions (Go forbids methods on a foreign-package type): every
// accessor that resolves an opaque PluginKinds body via a plugin's OpResolve leg (Distros/
// Builders/resolveInits below, and the Project*Config wrappers built on them), or that reaches
// the registered CandyScanner seam (ProjectCandies/projectCandiesScanned) — these need
// charly-core's OWN registry/seam access, which a separate sdk package cannot hold.

// Distros reconstructs the name-keyed per-distro build vocabulary from uf.PluginKinds.
// The `distro` kind is a plugin kind (candy/plugin-distro) — a `distro:` node (incl. the
// binary-embedded vocabulary) lands in uf.PluginKinds["distro"][<name>] as an OPAQUE
// canonical body. After the distro de-type (Cutover M) this accessor RESOLVES each body
// via candy/plugin-distro's OpResolve leg (resolveDistroViaPlugin) into a *DistroDef
// (= *spec.ResolvedDistro) — the build-engine value envelope the generator/format code
// consumes; the kernel never types spec.Distro. Recomputed per call; nil when no distros
// are configured; a bad entry is skipped rather than poisoning the whole vocabulary.
func Distros(uf *spec.UnifiedFile) map[string]*spec.ResolvedDistro {
	return spec.ResolvePluginKindViaPlugin(uf, "distro", resolveDistroViaPlugin)
}

// Builders reconstructs the name-keyed multi-stage builder vocabulary from
// uf.PluginKinds["builder"] (the `builder` plugin kind, candy/plugin-builder) into the
// map[string]*spec.BuilderDef shape the generator consumed when builder was a typed core map.
func Builders(uf *spec.UnifiedFile) map[string]*spec.BuilderDef {
	return spec.DecodePluginKindMap[spec.BuilderDef](uf, "builder")
}

// resolveInits projects the name-keyed init-system vocabulary from
// uf.PluginKinds["init"] (opaque bodies) into *ResolvedInit value envelopes via
// candy/plugin-init's OpResolve config leg (the init de-type, Cutover F) — the
// kernel never types the bodies.
func resolveInits(uf *spec.UnifiedFile) map[string]*ResolvedInit {
	return spec.ResolvePluginKindViaPlugin(uf, "init", resolveInitConfigViaPlugin)
}

// The build-vocab CONFIG projections (ProjectDistroConfig/ProjectBuilderConfig/ProjectInitConfig)
// moved to loaderkit (K3 Unit 1 — the ONE home charly core and candy/plugin-build both call, R3).
// charly callers invoke loaderkit.Project*Config(uf, <registry callback>) directly; the raw
// per-kind accessors (Distros/Builders/resolveInits) STAY here (they bind charly's in-proc registry
// OpResolve callbacks and are the map-shaped accessors the tests read).

// ProjectCandies scans or synthesizes a candy per entry in uf.Candy, into its FINAL
// spec.CandyReader form (W9: the type-Candy move). Thin wrapper over projectCandiesScanned +
// the ONE choke point (FinalizeScannedCandies, reached via the ProjectLoader seam, no InitCfg in scope
// for a standalone call) — see ScanAllCandyWithConfigOpts's doc comment for why completion never
// happens anywhere else.
func ProjectCandies(uf *spec.UnifiedFile, rootDir string) (map[string]spec.CandyReader, error) {
	scanned, err := projectCandiesScanned(uf, rootDir)
	if err != nil {
		return nil, err
	}
	return requireProjectLoader().FinalizeScannedCandies(scanned, nil), nil
}

// projectCandiesScanned is ProjectCandies' UNWRAPPED body: scans or synthesizes a candy per
// entry in uf.Candy, into its pre-completion, pre-finalize spec.ScannedCandy form. Entries with
// `from:` go through the registered loader plugin's typed CandyScanner seam so directory-based
// candies behave identically to today. Inline entries synthesize from the embedded CandyYAML and
// take their DECLARING FILE's directory as SourceDir — rootDir here, or the namespace sub-file's
// dir when one declares them (host_build_buildengine.go); see ScanInlineCandy's own contract.
func projectCandiesScanned(uf *spec.UnifiedFile, rootDir string) (map[string]spec.ScannedCandy, error) {
	out := map[string]spec.ScannedCandy{}
	for name, raw := range uf.Candy {
		il, ok := spec.DecodeInlineCandy(raw)
		if !ok {
			continue
		}
		if il.From != "" {
			// Directory-based candy — reuse existing scanner.
			p := il.From
			if !filepath.IsAbs(p) {
				p = filepath.Join(rootDir, p)
			}
			manifest := il.Manifest
			if manifest == "" {
				manifest = UnifiedFileName
			}
			m, v, refs, err := requireCandyScanner().ScanCandyManifest(p, name, manifest, parseCandyYAML)
			if err != nil {
				return nil, fmt.Errorf("candy %q from %q: %w", name, il.From, err)
			}
			// Candies discovered via `include:` of a remote charly.yml
			// live OUTSIDE the workspace's project tree (typically in
			// the github cache under ~/.cache/charly/repos/). Mark them as
			// Remote so the generator's createRemoteCandyCopies stages
			// them into .build/_candy/ and the emitted Containerfile
			// COPY paths resolve correctly. THIRD instance of the
			// construct-then-mutate-Remote pattern (W9 mutation-site inventory) —
			// distinct from loaderkit.ScanRemoteCandy's explicit-fetch case and
			// loaderkit.QualifyRemoteSiblingDeps's sibling-dep qualification: this
			// one fires on a plain `from:`-directory candy whose resolved path
			// happens to fall outside the project root.
			if absRoot, err := filepath.Abs(rootDir); err == nil {
				if absCandy, err := filepath.Abs(p); err == nil {
					if rel, err := filepath.Rel(absRoot, absCandy); err == nil && strings.HasPrefix(rel, "..") {
						v.Remote = true
					}
				}
			}
			out[name] = spec.ScannedCandy{Model: m, View: v, Refs: refs}
			continue
		}
		// Inline candy — synthesize. Always LOCAL (declared directly in this
		// charly.yml), so no remote-sibling qualification is needed — mirrors the
		// W9 spike's local-candy case.
		m, v, refs := requireCandyScanner().ScanInlineCandy(name, rootDir, &il.CandyYAML)
		out[name] = spec.ScannedCandy{Model: m, View: v, Refs: refs}
	}
	return out, nil
}
