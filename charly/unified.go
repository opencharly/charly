package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/spec"
	"github.com/opencharly/sdk/vmshared"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/loaderkit"
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
const UnifiedFileName = kit.UnifiedFileName

// The on-disk charly.yml schema version is a CalVer string (e.g.
// 2026.141.1530) — the same scheme as image tags. LatestSchemaVersion()
// (CUE-owned via spec.SchemaVersion) is the HEAD value; the LoadUnified gate
// refuses anything older with a hint pointing at `charly migrate`.

// UnifiedFile (K1 keystone, task #24 unit 1) relocated to sdk/loaderkit — see
// loaderkit.UnifiedFile. Every field type was already sdk-portable (spec.*/deploykit.*/plain
// maps; Box was already spec.BoxMap via the (now-deleted) boxMap alias, Candy already a plain
// map[string]json.RawMessage via the (now-deleted) candyMap alias), so the type carried NO
// charly-core dependency of its own. Every charly/*.go reference is now loaderkit.UnifiedFile.

// ImportEntry, ImportList, DiscoverConfig, and ScanSpec are the kind-blind
// config-loader document DIRECTIVE types — relocated to sdk/kit
// (loader_directives.go) so charly core AND sdk/loaderkit share ONE copy (R3).
// See kit.ImportEntry / kit.ImportList / kit.DiscoverConfig / kit.ScanSpec.

// InlineCandy (K1 keystone, task #24 unit 1) relocated to sdk/loaderkit — see
// loaderkit.InlineCandy.

// DeploymentsSection (the legacy v3 plural `deployments:` wrapper type) was
// DELETED as dead code (radical dead-code removal): after the field-singular
// cutover (2026-05) loaderkit.UnifiedFile.Bundle is a flat map and Provides moved
// to root level, and the last real referent — migrate_unified.go — is long gone.
// Its only surviving mentions were prose (deploy_tree.go's resolveTreeRoot doc
// comment, this file) plus the TestLoadUnified_DeploymentsSection name (which
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

// gateSchemaVersion / normalizeV4Aliases (K1 keystone, task #24 unit 2) relocated
// to sdk/loaderkit — see loaderkit.GateSchemaVersion (called only from
// loaderkit.LoadUnified now) / loaderkit.NormalizeV4Aliases (called directly by
// materialize.go's per-document fold).

// LoadUnified (K1 keystone, task #24 unit 2) is now a THIN WRAPPER: the
// kind-blind orchestration (bootstrap phase, schema gates, walk, materialize,
// venue flatten, member fold, descent stamp, the validation chain) relocated to
// loaderkit.LoadUnified. Every step that touches the provider registry, or is a
// standing K5-final-decision core file (foldMembers/validateMembers,
// bundle_members.go), is threaded through as a seam — this wrapper's only job is
// building that loaderkit.LoadSeams from charly-core's existing host-coupled
// functions.
func LoadUnified(dir string) (*loaderkit.UnifiedFile, bool, error) {
	return loaderkit.LoadUnified(dir, loaderkit.LoadSeams{
		RunBootstrapPhase:        runBootstrapPhase,
		WalkProject:              hostWalkProject,
		MaterializeLoadedProject: materializeLoadedProject,
		FlattenBundleVenues:      flattenBundleVenues,
		FoldMembers:              foldMembers,
		StampBundleDescents:      stampBundleDescents,
		ValidateEphemeral:        validateEphemeralUnified,
		ValidateCheckBeds:        validateCheckBeds,
		ValidateAndroidDevices:   validateAndroidDevices,
		ValidateMembers:          validateMembers,
		ValidatePreemptible:      validatePreemptibleUnified,
	})
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
			branch, e := kit.GitDefaultBranch(kit.RepoGitURL(parsed.RepoPath))
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
// mergeBoxConfig (K1-proper, task #24 follow-up) relocated to sdk/loaderkit
// (merge.go) — the kind-blind document MERGE half of the loader. They are pure
// map/struct merges over an already-parsed UnifiedFile with zero charly-core
// coupling (spec.*/kit.*/json only), so they belong in the sdk loaderkit consumed
// by the loader plugin, not in charly/ core (boundary law clause M). See
// loaderkit.MergeUnified (called from materialize.go) / loaderkit.MergePluginKindsMap
// (called from embed_defaults.go).

// anchorScanSpecs (kit.AnchorScanSpecs) is the discover-path anchoring helper
// — relocated to sdk/kit (loader_directives.go) so charly core AND
// sdk/loaderkit share ONE copy (R3).

// CheckBeds relocated to sdk/loaderkit (K1 keystone, task #24 unit 1) — see
// loaderkit.UnifiedFile.CheckBeds.

// validateCheckBeds enforces the kind:check bed-specific invariants beyond the
// generic deploy validation (which already runs on the folded beds via
// validateDeploymentTree → validateDeployRequiresBox, covering the pod
// `box:` requirement). Runs at LOAD time so EVERY command that resolves a
// bed (charly check run, charly bundle add, charly config, charly box validate, …) sees the
// same friendly error — not just `charly box validate`.
func validateCheckBeds(uf *loaderkit.UnifiedFile) error {
	for name, node := range uf.CheckBeds() {
		// An iterate: bed is a benchmark (the former kind:score), NOT a
		// deterministic R10 bed: it drives the AI loop scoring its plan's
		// check:/agent-check: steps against an operator-provisioned sandbox, so
		// the target/disposable/cross-ref requirements do not apply. Validate the
		// iterate block instead.
		if node.Iterate != nil {
			if err := validateIterateBed(uf, name, &node); err != nil {
				return err
			}
			continue
		}
		// Disposable is the sole authorization for the destroy+rebuild the
		// R10 sequence drives; a non-disposable bed can't be rebuilt
		// unattended (see /charly-internals:disposable).
		if !node.IsDisposable() {
			return fmt.Errorf(
				"kind:check bed %q must set `disposable: true` — `charly check run` destroys + rebuilds it unattended (R10 acceptance gate)",
				name)
		}
		// Bed-target validity is DATA-DRIVEN from the substrate's declared #DeployTraits
		// (candy/plugin-substrate), never a per-substrate-word switch (the boundary-law
		// incomplete-seam gate, task #22): bed_target marks pod/vm/local/android as valid bed
		// targets; k8s (bed_target:false) and unknown words fall to the external/unsupported arm.
		// image_backed distinguishes pod's box: cross-ref (enforced elsewhere) from the
		// template-backed vm/local/android from: cross-ref.
		traits := deployTraitsFor(node.Target)
		switch {
		case node.Target == "":
			// A GROUP bed (no workload cross-ref) — valid ONLY when it carries
			// sibling Members (subject + driver peers): the §3 group+siblings
			// shape for cross-deployment probing, where the driver venue is a
			// bare `${HOST:<subject>}` peer on the shared net (a peer requires a
			// group root in the tree-position model). The flattened plan
			// dispatches each step to its member venue; there is no root
			// container. Same spirit as the iterate-bed exemption above. A group
			// bed with neither a workload target nor members has nothing to run.
			if len(node.Members) == 0 {
				return fmt.Errorf("kind:check bed %q has no workload cross-ref and no sibling members — a group bed must declare member subdeployments (the subject + driver of a cross-deployment probe)", name)
			}
		case traits != nil && traits.BedTarget:
			// A valid bed target. image_backed (pod) enforces box: via
			// validateDeployRequiresBox on the folded Deploy entry — no duplicate check.
			// The template-backed substrates (vm/local/android) share ONE cross-ref shape:
			// a `from: <entity>` naming an entry in the SAME PluginKinds[target] map every
			// standalone-template kind folds into.
			if !traits.ImageBacked {
				if node.From == "" {
					return fmt.Errorf("kind:check bed %q (target: %s) must set `%s: <entity>`", name, node.Target, node.Target)
				}
				if _, ok := uf.PluginKinds[node.Target][node.From]; !ok {
					return fmt.Errorf("kind:check bed %q references %s entity %q which is not defined", name, node.Target, node.From)
				}
			}
		default:
			// An external (out-of-process) deploy substrate (e.g. `exampledeploy`):
			// the provider applies the deployment via the E3b reverse channel; it
			// composes its candies via add_candy: and carries no from:/image:
			// cross-ref to validate here. Recognized via a connected OR pre-scanned
			// EXTERNAL deploy provider (plugin_prescan.go) — NOT a core in-process
			// substrate (k8s has traits but bed_target:false, stays unsupported as a bed
			// target), so the bed validates before the provider connects (loadProjectPlugins).
			if isExternalDeploySubstrate(node.Target) {
				break
			}
			return fmt.Errorf("kind:check bed %q has unsupported target %q (must be pod, vm, local, android, or a registered external deploy substrate)", name, node.Target)
		}
	}
	return nil
}

// validateAndroidDevices enforces the kind:android device source invariant: a
// device is EXACTLY ONE of an in-pod emulator (box:) XOR a remote/physical adb
// endpoint (adb:) — never both, never neither. This is the entity-level XOR the
// #Android CUE schema formerly expressed via a trailing `& ({box:_} | {adb:_})`
// disjunction; that was dropped (gengotypes collapses an entity-level disjunction
// to an empty struct — see schema/android.cue) and the rule moved here. Runs at
// LOAD time alongside validateCheckBeds, so EVERY command that resolves a device
// (charly bundle add android:, charly check run, charly box validate, …) sees the
// same friendly error — the faithful breadth the CUE load-gate had.
func validateAndroidDevices(uf *loaderkit.UnifiedFile) error {
	if uf == nil {
		return nil
	}
	for name, spec := range resolveAndroids(uf) {
		if spec == nil {
			continue
		}
		hasBox := spec.Box != ""
		hasAdb := spec.Adb != nil
		switch {
		case hasBox && hasAdb:
			return fmt.Errorf("kind:android device %q sets both box: and adb: — a device is EXACTLY ONE of an in-pod emulator (box:) or a remote/physical adb endpoint (adb:)", name)
		case !hasBox && !hasAdb:
			return fmt.Errorf("kind:android device %q sets neither box: nor adb: — a device must declare EXACTLY ONE source (box: <kind:box emulator> or adb: {host: …})", name)
		}
	}
	return nil
}

// validateIterateBed enforces the iterate: benchmark invariants (replaces the
// former validateScoreNode/validateHarnessSemantics). An iterate bed is exempt
// from the deterministic R10 bed rules (target/disposable/cross-ref); instead:
//   - every iterate.agent[] entry references an entry in the `agent:` catalog;
//   - iterate.sandbox names a deployment (non-empty — its target kind is
//     resolved at run time, possibly against an operator-provisioned sandbox);
//   - the bed's plan: carries at least one `check:` step (the scored success
//     criteria — an include: step's checks expand at collect time, so a plan of
//     pure include: steps without a single direct check: is rejected here).
func validateIterateBed(uf *loaderkit.UnifiedFile, name string, node *spec.BundleNode) error {
	it := node.Iterate
	agents := uf.PluginKinds["agent"] // agent is a plugin kind; opaque name-keyed catalog
	for _, a := range it.Agent {
		if _, ok := agents[a]; !ok {
			return fmt.Errorf("iterate bed %q: agent %q is not defined in the agent: catalog", name, a)
		}
	}
	if strings.TrimSpace(it.Sandbox) == "" {
		return fmt.Errorf("iterate bed %q: iterate.sandbox must name a deployment (pod|vm|host) where the agent + charly run", name)
	}
	checks := 0
	for i := range node.Plan {
		if node.Plan[i].Check != "" {
			checks++
		}
	}
	if checks == 0 {
		return fmt.Errorf("iterate bed %q: plan must contain at least one `check:` step (the scored success criteria)", name)
	}
	return nil
}

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
// classify + gate + parse each manifest's documents) relocated to
// loaderkit.RunDiscover — the SAME walker.runDiscover/parseDiscoveredManifest
// mechanism Walk's own depth-0 discover pass already drives internally, reused
// here directly rather than duplicated. Only the registry-coupled MATERIALIZE
// fold (foldDiscoveredManifests, materialize.go — shared with
// materializeLoadedProject's own discovered-manifest step, R3) stays host-side.
func ApplyDiscover(uf *loaderkit.UnifiedFile, rootDir string) error {
	dms, err := loaderkit.RunDiscover(rootDir, uf.Discover, hostWalkSeams())
	if err != nil {
		return err
	}
	return foldDiscoveredManifests(dms, uf)
}

// findEntityDirs (kit.FindEntityDirs) + discoverSkipDir (kit.DiscoverSkipDir)
// are the discover-walk PRIMITIVES — relocated to sdk/kit
// (loader_discover.go) so charly core AND sdk/loaderkit share ONE copy (R3).

// -----------------------------------------------------------------------------
// Projections — extract the existing concrete types from loaderkit.UnifiedFile so the
// existing loaders can become thin wrappers.
// -----------------------------------------------------------------------------

// ProjectConfig/projectConfigCached/ProjectBundleConfig/VM/Pod/K8s/Local/Android/CheckBeds
// relocated to sdk/loaderkit as loaderkit.UnifiedFile methods (K1 keystone, task #24 unit 1) —
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
func Distros(uf *loaderkit.UnifiedFile) map[string]*spec.ResolvedDistro {
	return loaderkit.ResolvePluginKindViaPlugin(uf, "distro", resolveDistroViaPlugin)
}

// Builders reconstructs the name-keyed multi-stage builder vocabulary from
// uf.PluginKinds["builder"] (the `builder` plugin kind, candy/plugin-builder) into the
// map[string]*vmshared.BuilderDef shape the generator consumed when builder was a typed core map.
func Builders(uf *loaderkit.UnifiedFile) map[string]*vmshared.BuilderDef {
	return loaderkit.DecodePluginKindMap[vmshared.BuilderDef](uf, "builder")
}

// resolveInits projects the name-keyed init-system vocabulary from
// uf.PluginKinds["init"] (opaque bodies) into *ResolvedInit value envelopes via
// candy/plugin-init's OpResolve config leg (the init de-type, Cutover F) — the
// kernel never types the bodies.
func resolveInits(uf *loaderkit.UnifiedFile) map[string]*ResolvedInit {
	return loaderkit.ResolvePluginKindViaPlugin(uf, "init", resolveInitConfigViaPlugin)
}

// ProjectDistroConfig returns the *DistroConfig equivalent (distro: section), decoding
// the build vocabulary from the distro plugin kind (uf.PluginKinds via Distros(uf)).
func ProjectDistroConfig(uf *loaderkit.UnifiedFile) *buildkit.DistroConfig {
	distros := Distros(uf)
	if len(distros) == 0 {
		return nil
	}
	return &buildkit.DistroConfig{Distro: distros}
}

// ProjectBuilderConfig returns the *BuilderConfig equivalent (builders: section),
// decoding the build vocabulary from the builder plugin kind (uf.PluginKinds via
// Builders(uf)).
func ProjectBuilderConfig(uf *loaderkit.UnifiedFile) *buildkit.BuilderConfig {
	builders := Builders(uf)
	if len(builders) == 0 {
		return nil
	}
	return &buildkit.BuilderConfig{Builder: builders}
}

// ProjectInitConfig returns the *buildkit.InitConfig equivalent (inits: section), decoding
// the build vocabulary from the init plugin kind (uf.PluginKinds via resolveInits(uf)).
func ProjectInitConfig(uf *loaderkit.UnifiedFile) *buildkit.InitConfig {
	inits := resolveInits(uf)
	if len(inits) == 0 {
		return nil
	}
	return &buildkit.InitConfig{Init: inits}
}

// ProjectCandies scans or synthesizes a candy per entry in uf.Candy, into its FINAL
// spec.CandyReader form (W9: the type-Candy move). Thin wrapper over projectCandiesScanned +
// the ONE choke point (finalizeScannedCandies, no InitCfg in scope for a standalone call) —
// see ScanAllCandyWithConfigOpts's doc comment for why completion never happens anywhere else.
func ProjectCandies(uf *loaderkit.UnifiedFile, rootDir string) (map[string]spec.CandyReader, error) {
	scanned, err := projectCandiesScanned(uf, rootDir)
	if err != nil {
		return nil, err
	}
	return finalizeScannedCandies(scanned, nil), nil
}

// projectCandiesScanned is ProjectCandies' UNWRAPPED body: scans or synthesizes a candy per
// entry in uf.Candy, into its pre-completion, pre-finalize spec.ScannedCandy form. Entries with
// `from:` go through the registered loader plugin's typed CandyScanner seam so directory-based
// candies behave identically to today. Inline entries synthesize from the embedded CandyYAML
// (Part A's `directory:` field still applies).
func projectCandiesScanned(uf *loaderkit.UnifiedFile, rootDir string) (map[string]spec.ScannedCandy, error) {
	out := map[string]spec.ScannedCandy{}
	for name, raw := range uf.Candy {
		il, ok := loaderkit.DecodeInlineCandy(raw)
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
