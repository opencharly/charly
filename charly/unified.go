package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/opencharly/sdk/kit"
	"gopkg.in/yaml.v3"
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

// UnifiedFile is the full schema of a single unified-format YAML document.
// Every field is optional — a file with only `distro:` is valid (typical for
// the embedded build vocabulary, charly/charly.yml); a file with only `deploy:` is valid (typical
// for a charly.yml-style include); etc.
//
// Schema version 2 consolidates the legacy vms.yml + charly.yml split into one
// charly.yml file carrying both `vm:` (singular) and `deployments:` at the
// root. The top-level `vm:` key replaces the legacy `vms:` (plural). See
// `charly migrate` for the one-shot migration from v1.
type UnifiedFile struct {
	Version string `yaml:"version,omitempty" json:"version,omitempty"`
	// Repo is this project's canonical repo identity (e.g.
	// "github.com/opencharly/charly"). Optional; only meaningful on the ROOT
	// file. It lets the import-namespace loader break mutual-import cycles by
	// repo identity: a transitive import of THIS repo (at ANY pinned version)
	// resolves to the local working tree instead of fetching a divergent pinned
	// snapshot, so the root's namespace pins win. When unset, the loader falls
	// back to `git remote origin` inference (see ns_identity.go).
	Repo string `yaml:"repo,omitempty" json:"repo,omitempty"`
	// Import is the SINGLE composition statement (the legacy `include:` key
	// was deleted in the 2026-05 import-namespace cutover). A list whose
	// items are either a bare string (flat import into THIS root namespace —
	// same-repo file splits + shared build vocabulary) or a single-key
	// map `alias: ref` (a namespaced child import — cross-repo entity
	// cherry-pick, referenced qualified as `alias.entry`). See kit.ImportList.
	Import   kit.ImportList     `yaml:"import,omitempty" json:"import,omitempty"`
	Discover kit.DiscoverConfig `yaml:"discover,omitempty" json:"discover,omitempty"`
	// The build-vocabulary kinds (distro/builder/init) are no longer typed core maps:
	// each was extracted into a dedicated plugin kind (candy/plugin-distro /
	// candy/plugin-builder / the candy/plugin-init candy), so a `distro:`/`builder:`/`init:` node
	// (incl. the binary-embedded build vocabulary) lands in PluginKinds. The name-keyed
	// map[string]*XDef the generator/format code consumes is reconstructed on demand by
	// the Distros() / Builders() / Inits() accessors (RESOLVING the opaque bodies via each
	// kind plugin's OpResolve leg into the value envelope — DistroDef = spec.ResolvedDistro,
	// InitDef → ResolvedInit, …) and projected via ProjectDistroConfig /
	// ProjectBuilderConfig / ProjectInitConfig. The binary-embedded default vocabulary
	// merges UNDER a project's own entries via the generic root-wins mergePluginKindsMap
	// (applyEmbeddedDefaults). See unified.go Distros()/Builders()/Inits().
	Defaults BoxConfig `yaml:"defaults,omitempty" json:"defaults,omitempty"`
	// Field-singular cutover (2026-05): legacy plural `Images yaml:"images"`
	// deleted; the singular `Box yaml:"box"` is the canonical surface.
	// Box is the generic kind-keyed IMAGE map (P6): name → opaque marshaled BoxConfig; consumers
	// decode the authored BoxConfig via the accessors in uf_box_generic.go (the kernel holds no
	// per-kind TYPE). Config.Box shares this map.
	Box   boxMap                     `yaml:"box,omitempty" json:"box,omitempty"`
	Candy candyMap                   `yaml:"candy,omitempty" json:"candy,omitempty"`
	VM    map[string]json.RawMessage `yaml:"vm,omitempty" json:"vm,omitempty"`
	// Field-singular cutover: legacy `Deploys *DeploymentsSection
	// yaml:"deployments"` deleted. The flat `Bundle yaml:"deploy"` map is
	// the canonical singular surface; the wrapper's `Provides` migrates
	// to UnifiedFile root (next field).
	Bundle   map[string]BundleNode `yaml:"deploy,omitempty" json:"deploy,omitempty"`
	Provides *ProvidesConfig       `yaml:"provides,omitempty" json:"provides,omitempty"`

	// Schema v4: first-class target template maps (singular keys).
	// Pod (kind:pod) templates are stored OPAQUELY (the pod-template de-type,
	// Cutover J); the kernel never reads spec.Pod fields off the map — consuming
	// PLUGINS decode a body into the concrete kind they need.
	Pod map[string]json.RawMessage `yaml:"pod,omitempty" json:"pod,omitempty"`
	// K8s (kind:k8s) cluster templates are stored OPAQUELY (the k8s substrate-value
	// de-type, Cutover K) — resolved via resolveK8sViaPlugin; the full cluster model
	// rides opaquely to candy/plugin-k8sgen, never typed in the kernel.
	K8s map[string]json.RawMessage `yaml:"k8s,omitempty" json:"k8s,omitempty"`
	// Local (kind:local) templates are stored OPAQUELY (the substrate-template
	// de-type, Cutover I) — candy/plugin-substrate's OpResolve owns spec.Local;
	// the kernel resolves via uf.resolveLocals(), never reading fields off the map.
	Local map[string]json.RawMessage `yaml:"local,omitempty" json:"local,omitempty"`

	// Android (kind:android) — Android device substrates (an in-pod emulator
	// or a remote/physical adb endpoint) onto which `apk:` packages install
	// via a `target: android` deploy. Modeled on K8s (the device is the
	// substrate; the apps ride in on the deploy's candies). See android_spec.go.
	// Android (kind:android) templates are stored OPAQUELY (Cutover I) — resolved
	// via uf.resolveAndroids(); the kernel never reads spec.Android fields off the map.
	Android map[string]json.RawMessage `yaml:"android,omitempty" json:"android,omitempty"`

	// Agent catalog (kind:agent) — the AI-CLI graders the iterate loop drives — is a
	// dedicated plugin kind (candy/plugin-agent), so an `agent:` entity lands in
	// PluginKinds["agent"] as an OPAQUE body. The kernel never types it: the harness
	// resolves a generic spec.AgentExecSpec via candy/plugin-agent's OpResolve
	// (resolveAgentViaPlugin) — the agent de-type, Cutover E. See agent_config.go.

	// PluginKinds holds entities of KINDS contributed by plugins (a kind the core
	// has no typed map for). Decoded via the plugin's Invoke envelope
	// (runPluginKind) and stored as the plugin's canonical entity JSON, NAME-KEYED:
	// kind word → entity NAME (the node key) → canonical body. The entity body
	// itself stays NAMELESS (the node name is the top-level key, never an authored
	// body field), so #<Kind>Input is untouched; the NAME is mechanism metadata the
	// host threads from the node key into the storage key. Name-keyed so consumers
	// can look an entity up by name (the shape the Agents() / Sidecars() accessors
	// need) and so the merge is root-wins OVERRIDE (a project entity overrides an
	// embedded/imported one of the same name — e.g. a project `sidecar: tailscale`
	// over the embedded one) — see mergePluginKindsMap. Built-in
	// kinds decode into
	// their typed maps above. Host-internal — never serialized.
	PluginKinds map[string]map[string]json.RawMessage `yaml:"-" json:"-"`

	// A check bed is a `disposable: true` bundle in the Bundle map (the separate
	// kind:check block was removed in the node-form cutover); CheckBeds() derives
	// the R10 bed set from those disposable bundles. `charly check run <bed>`
	// drives the full R10 sequence.

	// Calamares-aligned kinds. The Calamares install `target:` (settings.conf), the
	// netinstall package group (`package-group:`), and the installer module (`module:`)
	// are no longer core typed maps — each was extracted into a dedicated plugin kind
	// (candy/plugin-target / candy/plugin-package-group / candy/plugin-module), so such an entity
	// lands in PluginKinds, not here. Calamares has zero core readers yet
	// (importers/emitters deferred), so — like module/package-group — `target` has no
	// accessor; the canonical body sits in PluginKinds for a future importer.

	// Resource (kind:resource) — exclusive host-resource definitions (a token name →
	// an optional gpu.vendor selector that drives GPU auto-allocation at `charly vm
	// create`) — is no longer a typed core map either: it was extracted into a dedicated
	// plugin kind (candy/plugin-resource), so a `resource:` node lands in PluginKinds. The
	// name-keyed map[string]*ResourceDef the GPU-arbitration code consumes is
	// reconstructed on demand by the Resources() accessor; the binary-embedded default
	// set merges UNDER a project's own entries via the generic mergePluginKindsMap.

	// Sidecar — the reusable sidecar-container template library — is a dedicated
	// plugin kind (candy/plugin-sidecar), so a `sidecar:` node (incl. the binary-
	// embedded `tailscale` template) lands in PluginKinds["sidecar"] as an OPAQUE
	// body. The kernel never types it: Config.Sidecar / BundleConfig.Sidecar carry
	// the raw bodies, and candy/plugin-sidecar's OpResolve owns ALL sidecar business
	// logic (merge + env-routing + resolution — the sidecar de-type, Cutover D). The
	// binary-embedded default set (e.g. `tailscale`) merges UNDER a project's own
	// entries via the generic root-wins mergePluginKindsMap (applyEmbeddedDefaults).
	// See sidecar.go, /charly-automation:sidecar.

	// Namespaces holds child namespaces mounted by namespaced `import:`
	// entries (alias → fully-resolved isolated UnifiedFile). NOT authored
	// directly and NOT flat-merged into the root maps — populated by
	// materializeLoadedProject (loader_driver.go) from the walk's namespace
	// mounts. Entries are referenced qualified, e.g.
	// `base: cachyos.cachyos` resolves `cachyos` in Namespaces, then its
	// Box["cachyos"]. Bare refs inside a namespace resolve within that
	// namespace first (Go package-member semantics). See charly/namespace.go.
	Namespaces map[string]*UnifiedFile `yaml:"-"`
}

// ImportEntry, ImportList, DiscoverConfig, and ScanSpec are the kind-blind
// config-loader document DIRECTIVE types — relocated to sdk/kit
// (loader_directives.go) so charly core AND sdk/loaderkit share ONE copy (R3).
// See kit.ImportEntry / kit.ImportList / kit.DiscoverConfig / kit.ScanSpec.

// InlineCandy is a candy declared inline in the unified file's `candy:` map.
// Mutually exclusive options: `from:` points at a directory to scan via the
// existing scanCandy (no schema change), OR the inline body defines the candy
// (same fields as the candy manifest, flattened via yaml:",inline").
type InlineCandy struct {
	From      string `yaml:"from,omitempty" json:"from,omitempty"`
	CandyYAML `yaml:",inline"`
	// Manifest carries the discovery manifest filename for a `From:` directory
	// so ProjectCandies→scanCandy reads the right file. Not YAML-authored; carried
	// through the opaque candy-map fold (P6) via JSON, hence exported + json-tagged.
	Manifest string `yaml:"-" json:"__manifest,omitempty"`
}

// DeploymentsSection carries repo-shipped deployment defaults plus per-image
// deployment entries. Matches the two-tier deploy model: this block is the
// authored/in-repo defaults; ~/.config/charly/charly.yml is the per-machine overlay.
// DeploymentsSection — RETIRED by the field-singular cutover (2026-05).
// UnifiedFile.Deploy is now a flat map; UnifiedFile.Provides moved to
// root level. The type definition is kept (not deleted) because
// migrate_unified.go still references it for legacy migration history.
type DeploymentsSection struct {
	Defaults *BundleNode           `yaml:"defaults,omitempty" json:"defaults,omitempty"`
	Provides *ProvidesConfig       `yaml:"provides,omitempty" json:"provides,omitempty"`
	Box      map[string]BundleNode `yaml:"box,omitempty" json:"box,omitempty"`
}

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

// gateSchemaVersion enforces the load-time schema-version contract: a config
// NEWER than this binary supports → "update charly"; an OLDER/absent/non-CalVer
// version → the `charly migrate` hint. Shared by the early pre-parse gate (root's
// raw version) and the post-merge gate (merged version) so both speak identically.
func gateSchemaVersion(root, version string) error {
	fileVer, verOK := ParseCalVer(version)
	switch {
	case verOK && LatestSchemaVersion().Less(fileVer):
		// Written for a NEWER schema than this binary understands; `charly migrate`
		// only moves forward to THIS binary's HEAD, so the binary itself is behind.
		return fmt.Errorf(
			"%s: config schema %s is newer than this charly supports (max %s). Update charly (reinstall the latest opencharly package, or run 'task build:charly' from a fresh checkout)",
			root, version, LatestSchemaVersion(),
		)
	case !verOK || fileVer.Less(LatestSchemaVersion()):
		return fmt.Errorf(
			"%s: schema %s is required (found %q). Run: charly migrate",
			root, LatestSchemaVersion(), version,
		)
	}
	return nil
}

func LoadUnified(dir string) (*UnifiedFile, bool, error) {
	root := filepath.Join(dir, UnifiedFileName)
	if !fileExists(root) {
		return nil, false, nil
	}
	// F9 BOOTSTRAP PHASE: invoke bootstrap-phase plugins on the RAW root config bytes FIRST — before
	// the early schema gate AND before the walk — so a bootstrap plugin's rewrite reaches both. The
	// transformed bytes are threaded into the walk as the root override so it PARSES them (not a
	// stale disk re-read). A read error is tolerated: the walk then reads the root from disk (no
	// bootstrap / early gate), exactly as before. Today only the no-op candy/plugin-example-bootstrap
	// registers here; a no-op (or none) returns the bytes unchanged → identity.
	var rootData []byte
	if data, rerr := os.ReadFile(root); rerr == nil {
		data = runBootstrapPhase(data)
		// EARLY schema-version gate: a below-HEAD (or absent) root `version:` is rejected with the
		// `charly migrate` hint BEFORE any shape parsing — so an out-of-date config never reaches
		// node-form CUE validation (a confusing type error instead of the migrate hint).
		var vdoc yaml.Node
		if yaml.Unmarshal(data, &vdoc) == nil {
			ver := ""
			if vn := mapValue(mappingRoot(&vdoc), "version"); vn != nil {
				ver = vn.Value
			}
			if err := gateSchemaVersion(root, ver); err != nil {
				return nil, true, err
			}
		}
		rootData = data
	}
	// THE KIND-BLIND WALK (sdk/loaderkit): import queue + discover + namespaced-import mounts +
	// per-document parse → a generic spec.LoadedProject. No materialize, no merge — those are the
	// registry-coupled host half below (boundary law). The root's repo-identity cycle-break seed +
	// the six kind-blind host seams live in hostWalkProject (loader_driver.go).
	lp, err := hostWalkProject(dir, rootData)
	if err != nil {
		return nil, true, err
	}
	// MATERIALIZE + root-wins MERGE (host, registry kind-decode) → the typed *UnifiedFile, exactly as
	// the former inline loadUnifiedInto did (materializeLoadedProject, loader_driver.go).
	merged := &UnifiedFile{}
	if err := materializeLoadedProject(&lp, merged, map[int64]*UnifiedFile{}); err != nil {
		return nil, true, err
	}
	normalizeV4Aliases(merged)
	if err := gateSchemaVersion(root, merged.Version); err != nil {
		return nil, true, err
	}
	// Stamp each plan step's execution VENUE from its bundle-tree position and
	// hoist member/child steps into the root bundle's flat Plan. MUST run before
	// foldMembers (which mutates the Bundle map by promoting members to
	// top-level) and before validateCheckBeds/validateIterateBed (which count
	// the root Plan's check: steps). After this, both runner entry points read
	// the venue-stamped root Plan.
	if err := flattenBundleVenues(merged); err != nil {
		return nil, true, fmt.Errorf("%s: %w", root, err)
	}
	// A check bed IS a `disposable: true` bundle in the Bundle map (the separate
	// kind:check block was removed in the node-form cutover) — no folding needed;
	// CheckBeds() derives the bed set from the disposable bundles directly.
	// Fold sibling members (companion deployments) into the Bundle map as
	// addressable top-level entries (inheriting the owner's disposability) so
	// the SAME deploy verbs bring them up/down. Runs BEFORE validateDeploymentTree
	// (so folded members get the same deploy validation). Agent-provisioned
	// members are SKIPPED by foldMembers (the AI deploys them in-run).
	if err := foldMembers(merged); err != nil {
		return nil, true, fmt.Errorf("%s: %w", root, err)
	}
	// Auto-promote disposable on ephemeral entries + validate the ephemeral /
	// vm-naming invariants. Consolidated here from the old per-host-only
	// LoadBundleConfig (R3 — one path), so the project charly.yml's inline
	// deploy: entries get the same promotion + checks. Runs after the folds so
	// folded beds/peers are covered, before validateDeploymentTree.
	// Stamp every deploy node's venue-hop descent-descriptor (the descent de-type,
	// Cutover H) — uniformly here, after ALL structural kinds have folded into
	// uf.Bundle, so the kernel's deploy chain descends by TRANSPORT and never
	// switches on the substrate kind word.
	stampBundleDescents(merged)
	if err := validateEphemeralUnified(merged); err != nil {
		return nil, true, fmt.Errorf("%s: %w", root, err)
	}
	if err := validateDeploymentTree(merged.Bundle); err != nil {
		return nil, true, fmt.Errorf("%s: %w", root, err)
	}
	if err := validateCheckBeds(merged); err != nil {
		return nil, true, fmt.Errorf("%s: %w", root, err)
	}
	if err := validateAndroidDevices(merged); err != nil {
		return nil, true, fmt.Errorf("%s: %w", root, err)
	}
	if err := validateMembers(merged); err != nil {
		return nil, true, fmt.Errorf("%s: %w", root, err)
	}
	if err := validatePreemptibleUnified(merged); err != nil {
		return nil, true, fmt.Errorf("%s: %w", root, err)
	}
	return merged, true, nil
}

// validateDeploymentTree enforces structural invariants on the deployments tree
// that can't be expressed in the YAML struct tags:
//
//   - Map keys at every level MUST NOT contain "." (dots are reserved
//     for dotted-path CLI addressing like `charly bundle add a.b.c`).
//   - Every explicit pod deploy must declare `box:` (validateDeployRequiresBox).
//
// Errors include the offending path so the user sees exactly which entry needs
// to be fixed.
func validateDeploymentTree(deploy map[string]BundleNode) error {
	if deploy == nil {
		return nil
	}
	for name, node := range deploy {
		if err := validateDeploymentName(name, ""); err != nil {
			return err
		}
		if err := validateDeploymentChildren(name, &node); err != nil {
			return err
		}
	}
	if err := validateDeployRequiresBox(deploy); err != nil {
		return err
	}
	return nil
}

// validateDeployRequiresBox enforces the 2026-05-12 schema rule:
// every `target: pod` deploy entry MUST declare its `box:` field.
// Pre-cutover the check runner silently fell back to inspecting the
// running container's image ref via `containerImageRef`, which read
// stale OCI labels off volume-pinned containers and dropped any
// probes added after the seed image. The hard-required field forces
// operator intent to be explicit; the check runner now resolves the
// ref ONLY from this field.
//
// Scope: target: pod (or empty — pod is the default). target: vm
// uses `vm:`, target: local is candy-driven, target: k8s
// CLUSTER definitions live in the `k8s:` section (not deploy:).
//
// Remediation: `charly migrate` (idempotent) walks every
// affected deploy and injects the field, inferring the value from
// the deploy key (`<base>` for `<base>/<instance>` keys; the key
// itself otherwise).
func validateDeployRequiresBox(deploy map[string]BundleNode) error {
	for name, node := range deploy {
		// An iterate: benchmark (the former kind:score) composes its scored
		// subject via plan `include:` steps + the iterate.sandbox, NOT a single
		// `box:`. It is exempt from the pod-target box requirement; its own
		// invariants are checked by validateCheckBeds (iterate block validation).
		if node.Iterate != nil {
			continue
		}
		// An agent-provisioned member carries NO box: by design — the AI builds
		// its image at run time (the iterate-benchmark contract). Exempt it from
		// the pod-target box requirement.
		if node.AgentProvisioned {
			continue
		}
		target := node.Target
		// Only an explicit pod-target (a `pod` node, or a `bundle` that inferred pod
		// from a box) is box-required. An EMPTY target is a group / per-host overlay
		// entry (no workload), never a pod-leaf — in node-form a real pod always
		// carries its box (the target is inferred FROM the box), so an empty target
		// can only be a group, which needs no box.
		if target != "pod" {
			continue
		}
		if node.Image == "" {
			// A bundle GROUP / venue (no own workload) carries members but no
			// box of its own — its member nodes each declare their box and are
			// validated as folded top-level entries. Only a LEAF pod-workload
			// (no members) must declare box.
			if len(node.Members) > 0 || len(node.Children) > 0 {
				continue
			}
			return fmt.Errorf(
				"deploy entry %q lacks required `box:` field — a pod-target deploy must declare `box:` explicitly (the check runner reads the operator's declared intent, not the running container's stale label)",
				name,
			)
		}
	}
	return nil
}

func validateDeploymentChildren(path string, node *BundleNode) error {
	if node == nil || len(node.Children) == 0 {
		return nil
	}
	for childName, child := range node.Children {
		childPath := childName
		if path != "" {
			childPath = path + "." + childName
		}
		if err := validateDeploymentName(childName, path); err != nil {
			return err
		}
		if err := validateDeploymentChildren(childPath, child); err != nil {
			return err
		}
	}
	return nil
}

func validateDeploymentName(name, parentPath string) error {
	full := name
	if parentPath != "" {
		full = parentPath + "." + name
	}
	if strings.Contains(name, ".") {
		return fmt.Errorf(
			"deployment key %q contains '.' — the character is reserved for dotted-path addressing (charly bundle add a.b.c). Rename this entry in charly.yml",
			full,
		)
	}
	return nil
}

// canonicalRef resolves an import ref (local path or
// `@host/org/repo[/sub/path]:version`) to a concrete on-disk path AND a stable
// cache key. Remote refs are downloaded into the shared repo cache (and
// auto-migrated). The key dedups identical refs across the whole load so a
// diamond — or the intentional main<->cachyos cycle — of namespaced imports
// resolves exactly once.
func canonicalRef(ref, baseDir string) (key, path string, err error) {
	if strings.HasPrefix(ref, "@") {
		parsed := ParseRemoteRef(ref)
		version := parsed.Version
		if version == "" {
			branch, e := GitDefaultBranch(RepoGitURL(parsed.RepoPath))
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

// normalizeV4Aliases — RETIRED by the field-singular cutover (2026-05).
// Dual `Images`/`ImageSingular` and `Deploys`/`DeploySingular` fields
// collapsed into single canonical singular fields with matching yaml
// tags. Function kept as a no-op so external callers don't break;
// remove on next refactor pass.
func normalizeV4Aliases(u *UnifiedFile) {
	_ = u
}

// mergeUnified merges src into dst such that dst's existing values WIN on
// conflict at the same leaf (root-wins). This means when materializeLoadedProject
// replays the walk's documents in order (the root file first, then its flat
// imports), the root file's values are already present before any import's
// fields are considered, so root wins.
//
// For included files: the same mergeUnified is called but dst already contains
// the root's values, so those fields stay untouched. src's fields that aren't
// present in dst get copied over. That's the desired semantics.
func mergeUnified(dst, src *UnifiedFile, srcDir string) {
	if src.Version != "" && dst.Version == "" {
		dst.Version = src.Version
	}
	// Root-wins: the root file (merged first) defines the project's repo
	// identity; a flat import declaring `repo:` never overrides it.
	if src.Repo != "" && dst.Repo == "" {
		dst.Repo = src.Repo
	}
	// Discover entries concatenate (not overwrite). Resolve relative
	// paths to absolute against srcDir so an included file's discover
	// roots remain anchored to the included file's directory rather
	// than to the eventual root file's directory. Without this, a
	// downstream workspace that `include:`-s an upstream charly.yml
	// would look for upstream's `candy/` inside the workspace tree.
	if len(src.Discover) > 0 {
		dst.Discover = append(dst.Discover, kit.AnchorScanSpecs(src.Discover, srcDir)...)
	}
	mergeRawTemplateMap(&dst.Box, src.Box)
	mergeRawTemplateMap(&dst.Candy, src.Candy)
	mergeRawTemplateMap(&dst.VM, src.VM)
	mergeRawTemplateMap(&dst.Pod, src.Pod)
	mergeRawTemplateMap(&dst.K8s, src.K8s)
	mergeRawTemplateMap(&dst.Local, src.Local)
	mergeRawTemplateMap(&dst.Android, src.Android)
	// PluginKinds carries every plugin-extracted kind — the build vocabulary
	// (distro/builder/init/resource), the Calamares target, and sidecar/agent/module/
	// package-group — merged once here (root-wins, name-keyed override). The former
	// mergeDistroMap/mergeBuilderMap/mergeInitMap/mergeResourceMap/mergeTargetMap calls
	// are subsumed by this one generic merge.
	mergePluginKindsMap(&dst.PluginKinds, src.PluginKinds)
	mergeDeployMaps(&dst.Bundle, src.Bundle)
	if dst.Provides == nil && src.Provides != nil {
		dst.Provides = src.Provides
	}
	// Defaults: dst wins per-field if set.
	mergeBoxConfig(&dst.Defaults, &src.Defaults)
}

// anchorScanSpecs (kit.AnchorScanSpecs) is the discover-path anchoring helper
// — relocated to sdk/kit (loader_directives.go) so charly core AND
// sdk/loaderkit share ONE copy (R3).

// mergeRawTemplateMap root-wins merges an OPAQUE substrate-template map (local /
// android after the Cutover I de-type): copy a name only when ABSENT in dst. One
// generic helper for both (R3) — the former typed mergeLocalMap/mergeAndroidMap.
func mergeRawTemplateMap(dst *map[string]json.RawMessage, src map[string]json.RawMessage) {
	if len(src) == 0 {
		return
	}
	if *dst == nil {
		*dst = make(map[string]json.RawMessage)
	}
	for k, v := range src {
		if _, exists := (*dst)[k]; !exists {
			(*dst)[k] = v
		}
	}
}

// mergePluginKindsMap merges plugin-contributed kind entities (uf.PluginKinds:
// kind word → entity NAME → canonical entity JSON) across every merged
// document/file. Root-wins NAME-KEYED OVERRIDE, byte-identical in spirit to the
// build-vocab map merges (mergeDistroMap et al.): for each kind, an existing dst
// entry for a given name is PRESERVED and src fills only the names dst does not have.
// So a project's entity overrides an embedded/imported one of the same name (one
// entry, not two) — the property the agent + sidecar extractions rely on (a project's
// `sidecar: tailscale` overriding the binary-embedded one, merged in via
// applyEmbeddedDefaults). Without this,
// plugin-kind entities decoded into a per-document `sub` UnifiedFile are silently
// dropped at mergeUnified (every document flows through here).
func mergePluginKindsMap(dst *map[string]map[string]json.RawMessage, src map[string]map[string]json.RawMessage) {
	if len(src) == 0 {
		return
	}
	if *dst == nil {
		*dst = make(map[string]map[string]json.RawMessage)
	}
	for kind, entities := range src {
		d := (*dst)[kind]
		if d == nil {
			d = make(map[string]json.RawMessage)
			(*dst)[kind] = d
		}
		for name, body := range entities {
			if _, exists := d[name]; !exists {
				d[name] = body
			}
		}
	}
}

// mergeDeployMaps merges src into dst, dst-wins on name collisions.
// Field-singular cutover: replaces the legacy mergeDeployments which
// took *DeploymentsSection wrappers. Provides now lives at UnifiedFile
// root and is merged separately by mergeUnified.
func mergeDeployMaps(dst *map[string]BundleNode, src map[string]BundleNode) {
	if len(src) == 0 {
		return
	}
	if *dst == nil {
		*dst = make(map[string]BundleNode)
	}
	for k, v := range src {
		if _, exists := (*dst)[k]; !exists {
			(*dst)[k] = v
		}
	}
}

// CheckBeds returns the disposable R10 beds keyed by name. In the unified
// node-form model a bed IS a `disposable: true` bundle (the separate kind:check
// block is gone), so the bed set is derived directly from the disposable
// bundles in the Bundle map. Members are instruments (brought up alongside a
// driver), never standalone beds. Single enumeration source for
// `charly check run <bed>` (and the /verify-beds fan-out).
func (uf *UnifiedFile) CheckBeds() map[string]BundleNode {
	if uf == nil {
		return nil
	}
	beds := map[string]BundleNode{}
	for name, node := range uf.Bundle {
		if node.IsDisposable() && node.MemberOf == "" {
			beds[name] = node
		}
	}
	return beds
}

// validateCheckBeds enforces the kind:check bed-specific invariants beyond the
// generic deploy validation (which already runs on the folded beds via
// validateDeploymentTree → validateDeployRequiresBox, covering the pod
// `box:` requirement). Runs at LOAD time so EVERY command that resolves a
// bed (charly check run, charly bundle add, charly config, charly box validate, …) sees the
// same friendly error — not just `charly box validate`.
func validateCheckBeds(uf *UnifiedFile) error {
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
		switch node.Target {
		case "":
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
		case "pod":
			// box: presence enforced by validateDeployRequiresBox on the
			// folded Deploy entry — no duplicate check here.
		case "vm":
			if node.From == "" {
				return fmt.Errorf("kind:check bed %q (target: vm) must set `vm: <entity>`", name)
			}
			if _, ok := uf.VM[node.From]; !ok {
				return fmt.Errorf("kind:check bed %q references vm entity %q which is not defined", name, node.From)
			}
		case "local":
			if node.From == "" {
				return fmt.Errorf("kind:check bed %q (target: local) must set `local: <template>`", name)
			}
			if _, ok := uf.Local[node.From]; !ok {
				return fmt.Errorf("kind:check bed %q references local template %q which is not defined", name, node.From)
			}
		case "android":
			if node.From == "" {
				return fmt.Errorf("kind:check bed %q (target: android) must set `android: <device>`", name)
			}
			if _, ok := uf.Android[node.From]; !ok {
				return fmt.Errorf("kind:check bed %q references android device %q which is not defined", name, node.From)
			}
		default:
			// An external (out-of-process) deploy substrate (e.g. `exampledeploy`):
			// the provider applies the deployment via the E3b reverse channel; it
			// composes its candies via add_candy: and carries no from:/image:
			// cross-ref to validate here. Recognized via a connected OR pre-scanned
			// EXTERNAL deploy provider (plugin_prescan.go) — NOT a core in-process
			// substrate (k8s stays unsupported as a bed target), so the bed validates
			// before the provider connects (loadProjectPlugins).
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
func validateAndroidDevices(uf *UnifiedFile) error {
	if uf == nil {
		return nil
	}
	for name, spec := range uf.resolveAndroids() {
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
func validateIterateBed(uf *UnifiedFile, name string, node *BundleNode) error {
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

// mergeBoxConfig preserves dst's already-set fields and fills only the
// zero-valued ones from src. Used for merging Defaults blocks from includes.
func mergeBoxConfig(dst, src *BoxConfig) {
	if src == nil || dst == nil {
		return
	}
	if dst.Base == "" {
		dst.Base = src.Base
	}
	if dst.Tag == "" {
		dst.Tag = src.Tag
	}
	if dst.Registry == "" {
		dst.Registry = src.Registry
	}
	if len(dst.Platforms) == 0 {
		dst.Platforms = src.Platforms
	}
	if len(dst.Distro) == 0 {
		dst.Distro = src.Distro
	}
	if len(dst.Build) == 0 {
		dst.Build = src.Build
	}
	if len(dst.Candy) == 0 {
		dst.Candy = src.Candy
	}
	if dst.User == "" {
		dst.User = src.User
	}
	if dst.UID == nil {
		dst.UID = src.UID
	}
	if dst.GID == nil {
		dst.GID = src.GID
	}
	if dst.UserPolicy == "" {
		dst.UserPolicy = src.UserPolicy
	}
	if dst.Merge == nil {
		dst.Merge = src.Merge
	}
	if len(dst.Builder) == 0 {
		dst.Builder = src.Builder
	}
	if dst.Init == "" {
		dst.Init = src.Init
	}
	// Build-speed tunables (defaults: block) — carried through the same
	// per-field "dst wins if set" merge as the rest of BoxConfig.
	if dst.Jobs == nil {
		dst.Jobs = src.Jobs
	}
	if dst.PodmanJobs == nil {
		dst.PodmanJobs = src.PodmanJobs
	}
	if dst.PodmanJobsCap == nil {
		dst.PodmanJobsCap = src.PodmanJobsCap
	}
	if len(dst.ContextIgnore) == 0 {
		dst.ContextIgnore = src.ContextIgnore
	}
	if dst.Cache == "" {
		dst.Cache = src.Cache
	}
	if dst.KeepImages == nil {
		dst.KeepImages = src.KeepImages
	}
	if dst.KeepCheckRuns == nil {
		dst.KeepCheckRuns = src.KeepCheckRuns
	}
}

// -----------------------------------------------------------------------------
// Discovery scanner (Part D).
// -----------------------------------------------------------------------------

// ApplyDiscover walks every flat scan spec on uf.Discover and registers any
// entity found. Each spec scans its path for directories containing the spec's
// manifest; every discovered manifest is routed by SHAPE. Conflict rule:
// explicit map entries win over discovered entries. scanRoot resolution is
// relative to rootDir (the dir containing charly.yml).
func (uf *UnifiedFile) ApplyDiscover(rootDir string) error {
	for _, s := range uf.Discover {
		manifest := s.Manifest
		if manifest == "" {
			manifest = UnifiedFileName
		}
		scanPath := s.Path
		if !filepath.IsAbs(scanPath) {
			scanPath = filepath.Join(rootDir, scanPath)
		}
		dirs, err := kit.FindEntityDirs(scanPath, manifest, s.Recursive)
		if err != nil {
			return fmt.Errorf("discover %q: %w", s.Path, err)
		}
		for _, d := range dirs {
			if err := uf.applyDiscoveredManifest(d, manifest, rootDir); err != nil {
				return err
			}
		}
	}
	return nil
}

// findEntityDirs (kit.FindEntityDirs) + discoverSkipDir (kit.DiscoverSkipDir)
// are the discover-walk PRIMITIVES — relocated to sdk/kit
// (loader_discover.go) so charly core AND sdk/loaderkit share ONE copy (R3).

// applyDiscoveredManifest loads one discovered manifest and routes every
// document it contains by SHAPE through the SAME classifier the main loader uses
// (kit.ClassifyDoc): a legacy kind-keyed / root-shape manifest is hard-rejected with
// a `charly migrate` hint, an empty/directive-only doc is skipped, and a unified
// node-form doc is validated against #NodeDoc (the sole grammar gate) before its
// entities are registered. A `candy` node registers a lazy `From:` directory
// reference (scanCandy parses + validates the manifest and resolves the candy's
// assets relative to its dir); every other kind normalizes inline. The conflict
// rule "explicit entry wins" applies to discovered candies.
func (uf *UnifiedFile) applyDiscoveredManifest(dir, manifest, rootDir string) error {
	target := filepath.Join(dir, manifest)
	data, err := os.ReadFile(target)
	if err != nil {
		return fmt.Errorf("reading %s: %w", target, err)
	}
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	for {
		var node yaml.Node
		if err := decoder.Decode(&node); err != nil {
			if err.Error() == "EOF" {
				break
			}
			return fmt.Errorf("%s: %w", target, err)
		}
		shape, cerr := kit.ClassifyDoc(&node)
		if cerr != nil {
			return fmt.Errorf("%s: %w", target, cerr)
		}
		if shape == kit.DocShapeEmpty {
			continue // empty / directive-only document — nothing to register
		}
		// VALIDATE-BEFORE-EXECUTE: the whole node-form manifest against #NodeDoc
		// (strict + closed) — the SAME #NodeDoc gate the walk's GateDoc seam applies to
		// the root charly.yml, so #NodeDoc is the sole load-time gate for EVERY loaded
		// document, discovered manifests included.
		raw, merr := yaml.Marshal(&node)
		if merr != nil {
			return fmt.Errorf("%s: re-marshal node-form doc: %w", target, merr)
		}
		if verr := validateNodeDocCUE(target, raw); verr != nil {
			return verr
		}
		// The ONE node-form parse is the registered config front-end (P6, sdk/loaderkit); the
		// genericNode the candy pre-check + normalizeNodeInto consume is reconstructed per node.
		_, pp, perr := requireLoaderParser().ParseDoc(&node, loaderThreaded())
		if perr != nil {
			// A malformed node-form manifest is a HARD error, never silently
			// dropped (a swallowed parse error would discover "0 candies").
			return fmt.Errorf("%s: %w", target, perr)
		}
		for i := range pp.Nodes {
			gn, gerr := parsedNodeToGeneric(pp.Nodes[i])
			if gerr != nil {
				return fmt.Errorf("%s: %w", target, gerr)
			}
			// The SAME per-node discovered-fold the LoadUnified walk path uses
			// (materializeDiscoveredNode, loader_driver.go) — a LAYER candy registers a lazy
			// `From:` reference (explicit entry wins), every other kind materializes inline. R3:
			// one discovered-node handler for both the walk path and this candy-scan path.
			if err := materializeDiscoveredNode(gn, dir, rootDir, manifest, uf); err != nil {
				return fmt.Errorf("%s: %w", target, err)
			}
		}
	}
	return nil
}

// -----------------------------------------------------------------------------
// Projections — extract the existing concrete types from UnifiedFile so the
// existing loaders can become thin wrappers.
// -----------------------------------------------------------------------------

// ProjectConfig returns the *Config equivalent of uf (the box config view).
func (uf *UnifiedFile) ProjectConfig() *Config {
	return uf.projectConfigCached(map[*UnifiedFile]*Config{})
}

// projectConfigCached projects uf (and its import namespaces, recursively) into
// a *Config. The pointer-keyed cache breaks the intentional main<->cachyos
// import cycle (the shared UnifiedFile node is projected exactly once).
func (uf *UnifiedFile) projectConfigCached(cache map[*UnifiedFile]*Config) *Config {
	if c, ok := cache[uf]; ok {
		return c
	}
	images := uf.Box
	if images == nil {
		images = boxMap{}
	}
	c := &Config{
		Defaults: uf.Defaults,
		Box:      images,
		Local:    uf.Local,
		Sidecar:  uf.PluginKinds["sidecar"], // opaque bodies; candy/plugin-sidecar resolves them
	}
	cache[uf] = c // cache BEFORE recursing (cycle break)
	if len(uf.Namespaces) > 0 {
		c.Namespaces = make(map[string]*Config, len(uf.Namespaces))
		for ns, sub := range uf.Namespaces {
			c.Namespaces[ns] = sub.projectConfigCached(cache)
		}
	}
	return c
}

// Distros reconstructs the name-keyed per-distro build vocabulary from uf.PluginKinds.
// The `distro` kind is a plugin kind (candy/plugin-distro) — a `distro:` node (incl. the
// binary-embedded vocabulary) lands in uf.PluginKinds["distro"][<name>] as an OPAQUE
// canonical body. After the distro de-type (Cutover M) this accessor RESOLVES each body
// via candy/plugin-distro's OpResolve leg (resolveDistros) into a *DistroDef
// (= *spec.ResolvedDistro) — the build-engine value envelope the generator/format code
// consumes; the kernel never types spec.Distro. Recomputed per call; nil when no distros
// are configured; a bad entry is skipped rather than poisoning the whole vocabulary.
func (uf *UnifiedFile) Distros() map[string]*DistroDef {
	return uf.resolveDistros()
}

// Builders reconstructs the name-keyed multi-stage builder vocabulary from
// uf.PluginKinds["builder"] (the `builder` plugin kind, candy/plugin-builder) into the
// map[string]*BuilderDef shape the generator consumed when builder was a typed core map.
func (uf *UnifiedFile) Builders() map[string]*BuilderDef {
	return decodePluginKindMap[BuilderDef](uf, "builder")
}

// resolveInits projects the name-keyed init-system vocabulary from
// uf.PluginKinds["init"] (opaque bodies) into *ResolvedInit value envelopes via
// candy/plugin-init's OpResolve config leg (the init de-type, Cutover F) — the
// kernel never types the bodies. A bad entry is skipped rather than poisoning the
// vocabulary (cf. decodePluginKindMap).
func (uf *UnifiedFile) resolveInits() map[string]*ResolvedInit {
	if uf == nil {
		return nil
	}
	bodies := uf.PluginKinds["init"]
	if len(bodies) == 0 {
		return nil
	}
	out := make(map[string]*ResolvedInit, len(bodies))
	for name, body := range bodies {
		ri, err := resolveInitConfigViaPlugin(body)
		if err != nil || ri == nil {
			continue
		}
		out[name] = ri
	}
	return out
}

// decodePluginKindMap reconstructs the typed name-keyed map[string]*T for a plugin kind
// from uf.PluginKinds[kind] (each body the canonical spec.T JSON the kind plugin's Invoke
// produced). Shared by the build-vocabulary accessors (Distros/Builders/Inits/Resources)
// — the build-vocab analogue of Agents()/Sidecars(); a bad entry is skipped rather than
// poisoning the whole vocabulary. Returns nil when the kind has no entities.
func decodePluginKindMap[T any](uf *UnifiedFile, kind string) map[string]*T {
	if uf == nil {
		return nil
	}
	bodies := uf.PluginKinds[kind]
	if len(bodies) == 0 {
		return nil
	}
	out := make(map[string]*T, len(bodies))
	for name, body := range bodies {
		var v T
		if err := json.Unmarshal(body, &v); err != nil {
			continue
		}
		out[name] = &v
	}
	return out
}

// ProjectDistroConfig returns the *DistroConfig equivalent (distro: section), decoding
// the build vocabulary from the distro plugin kind (uf.PluginKinds via Distros()).
func (uf *UnifiedFile) ProjectDistroConfig() *DistroConfig {
	distros := uf.Distros()
	if len(distros) == 0 {
		return nil
	}
	return &DistroConfig{Distro: distros}
}

// ProjectBuilderConfig returns the *BuilderConfig equivalent (builders: section),
// decoding the build vocabulary from the builder plugin kind (uf.PluginKinds via
// Builders()).
func (uf *UnifiedFile) ProjectBuilderConfig() *BuilderConfig {
	builders := uf.Builders()
	if len(builders) == 0 {
		return nil
	}
	return &BuilderConfig{Builder: builders}
}

// ProjectInitConfig returns the *InitConfig equivalent (inits: section), decoding the
// build vocabulary from the init plugin kind (uf.PluginKinds via Inits()).
func (uf *UnifiedFile) ProjectInitConfig() *InitConfig {
	inits := uf.resolveInits()
	if len(inits) == 0 {
		return nil
	}
	return &InitConfig{Init: inits}
}

// ProjectBundleConfig returns the *BundleConfig equivalent (deployments: section
// of the authored file, independent of any per-machine ~/.config/charly/charly.yml
// which remains loaded separately by LoadBundleConfig).
func (uf *UnifiedFile) ProjectBundleConfig() *BundleConfig {
	if uf == nil {
		return nil
	}
	sidecars := uf.PluginKinds["sidecar"] // opaque bodies; candy/plugin-sidecar resolves them
	if len(uf.Bundle) == 0 && uf.Provides == nil && len(sidecars) == 0 {
		return nil
	}
	return &BundleConfig{
		Provides: uf.Provides,
		Bundle:   uf.Bundle,
		Sidecar:  sidecars,
	}
}

// ProjectCandies scans or synthesizes a *Candy per entry in uf.Candy. Entries
// with `from:` go through the existing scanCandy so directory-based candies
// behave identically to today. Inline entries synthesize a *Candy from the
// embedded CandyYAML (Part A's `directory:` field still applies).
func (uf *UnifiedFile) ProjectCandies(rootDir string) (map[string]*Candy, error) {
	out := map[string]*Candy{}
	for name, raw := range uf.Candy {
		il, ok := decodeInlineCandy(raw)
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
			layer, err := scanCandy(p, name, manifest)
			if err != nil {
				return nil, fmt.Errorf("candy %q from %q: %w", name, il.From, err)
			}
			// Candies discovered via `include:` of a remote charly.yml
			// live OUTSIDE the workspace's project tree (typically in
			// the github cache under ~/.cache/charly/repos/). Mark them as
			// Remote so the generator's createRemoteCandyCopies stages
			// them into .build/_candy/ and the emitted Containerfile
			// COPY paths resolve correctly.
			if absRoot, err := filepath.Abs(rootDir); err == nil {
				if absCandy, err := filepath.Abs(p); err == nil {
					if rel, err := filepath.Rel(absRoot, absCandy); err == nil && strings.HasPrefix(rel, "..") {
						layer.Remote = true
					}
				}
			}
			out[name] = layer
			continue
		}
		// Inline candy — synthesize.
		out[name] = synthesizeInlineCandy(name, il, rootDir)
	}
	return out, nil
}

// synthesizeInlineCandy produces a *Candy from an inline declaration in the
// unified file. The effective Path is rootDir (the charly.yml's dir);
// SourceDir always equals Path (the `directory:` field was deleted in the
// 2026-05 Calamares cutover).
func synthesizeInlineCandy(name string, il *InlineCandy, rootDir string) *Candy {
	// Use inline candy body as if it were a parsed candy manifest at rootDir.
	layer := &Candy{
		Name: name,
		Path: rootDir,
	}
	layer.SourceDir = rootDir
	// Populate fields the same way scanCandy does post-parse. We reuse the
	// logic by duplicating the minimal set a test would notice; the full set
	// can be factored out alongside Part G's refactor.
	populateCandyFromYAML(layer, &il.CandyYAML)
	// Install-file detection against SourceDir.
	layer.HasPixiToml = fileExists(filepath.Join(layer.SourceDir, "pixi.toml"))
	layer.HasPyprojectToml = fileExists(filepath.Join(layer.SourceDir, "pyproject.toml"))
	layer.HasEnvironmentYml = fileExists(filepath.Join(layer.SourceDir, "environment.yml"))
	layer.HasPackageJson = fileExists(filepath.Join(layer.SourceDir, "package.json"))
	layer.HasCargoToml = fileExists(filepath.Join(layer.SourceDir, "Cargo.toml"))
	layer.HasSrcDir = dirExists(filepath.Join(layer.SourceDir, "src"))
	layer.HasPixiLock = fileExists(filepath.Join(layer.SourceDir, "pixi.lock"))
	svcFiles, _ := filepath.Glob(filepath.Join(layer.SourceDir, "*.service"))
	if len(svcFiles) > 0 {
		layer.serviceFiles = svcFiles
	}
	return layer
}

// populateCandyFromYAML copies every field from a parsed CandyYAML into the
// runtime Candy. It is the SINGLE post-parse populator: BOTH scanCandy (the
// discovered-candy-dir path) and synthesizeInlineCandy (the charly.yml
// inline path) call it, so the two can never drift. (They previously did — the
// inline path silently dropped artifacts/capabilities/requiresCapabilities/
// shell and the unexported description.) The caller is responsible for the
// install-file filesystem probes (HasPixiToml etc.) against SourceDir.
func populateCandyFromYAML(layer *Candy, ly *CandyYAML) {
	layer.Version = ly.Version
	layer.Description = ly.Description
	layer.Status = ly.Status
	layer.Info = descriptionInfo(ly.Description)
	layer.Plugin = ly.Plugin

	layer.Require = toCandyRefs(ly.Require)
	layer.IncludedCandy = toCandyRefs(ly.Candy)
	layer.BakePlugin = toCandyRefs(ly.BakePlugin)

	// `bake_plugin: <ref>` IMPLIES `require: <ref>`. A baked plugin candy is
	// host-built and COPYed into every composing image (generate.go
	// emitBakedPlugins), but the COPY alone does not pull it into the require
	// chain — so its version: would NOT reach the composing image's
	// EffectiveVersion (effective_version.go walks the require-resolved candy set
	// via collectAllBoxCandies → ResolveCandyOrder over Require). Without the
	// implication a changed baked plugin whose own version: bumped but no other
	// layer's did leaves EffectiveVersion (the ai.opencharly.version label)
	// unchanged, so charly clean retention + short-name resolution treat it as
	// the same image and a stale baked plugin escapes rebuild. Folding the baked
	// plugin candy into require also makes it reach the scanned candy set (the
	// same path qualifyRemoteSiblingDeps documents for the baked-binary lookup).
	// Dedupe by bare (map-key) name so a candy declaring BOTH does not double-add.
	for _, bp := range layer.BakePlugin {
		already := false
		for _, req := range layer.Require {
			if req.Bare() == bp.Bare() {
				already = true
				break
			}
		}
		if !already {
			layer.Require = append(layer.Require, bp)
		}
	}

	layer.service = ly.Service
	// derivePackageSectionsFromCalamares is the SOLE populator of the package
	// surface (layer.tagSections + layer.topPackages, plus the arch `aur` format
	// section) from package: + distro:. There is no top-level format/tag-key
	// parse path anymore — the `distro:` map is the only package surface.
	if len(ly.Package) > 0 || len(ly.Distro) > 0 {
		derivePackageSectionsFromCalamares(layer, ly)
	}
	if len(ly.Port) > 0 {
		layer.ports = make([]string, len(ly.Port))
		layer.portSpecs = make([]PortSpec, len(ly.Port))
		for i, p := range ly.Port {
			if p.Protocol == "udp" {
				layer.ports[i] = fmt.Sprintf("%d/udp", p.Port)
			} else {
				layer.ports[i] = fmt.Sprintf("%d", p.Port)
			}
			layer.portSpecs[i] = p
		}
	}
	if len(ly.Env) > 0 || len(ly.PathAppend) > 0 {
		env := ly.Env
		if env == nil {
			env = make(map[string]string)
		}
		layer.envConfig = &EnvConfig{Vars: env, PathAppend: ly.PathAppend}
	}
	if ly.Route != nil {
		layer.route = &RouteConfig{Host: ly.Route.Host, Port: fmt.Sprintf("%d", ly.Route.Port)}
	}
	layer.volumes = ly.Volume
	layer.aliases = ly.Alias
	layer.extract = ly.Extract
	layer.data = ly.Data
	layer.security = ly.Security
	layer.libvirt = ly.Libvirt
	layer.hooks = ly.Hook
	layer.plan = ly.Plan
	layer.artifacts = ly.Artifact
	layer.capabilities = ly.Capability
	layer.requiresCapabilities = ly.RequiresCapability
	layer.PortRelayPorts = ly.PortRelay
	layer.secrets = ly.SecretYAML
	layer.envProvides = ly.EnvProvides
	layer.envRequires = ly.EnvRequire
	layer.envAccepts = ly.EnvAccept
	layer.secretAccepts = ly.SecretAccept
	layer.secretRequires = ly.SecretRequire
	layer.mcpProvides = ly.MCPProvide
	layer.mcpRequires = ly.MCPRequire
	layer.mcpAccepts = ly.MCPAccept
	layer.engine = ly.Engine
	layer.vars = ly.Vars
	layer.apk = ly.Apk
	layer.localpkg = ly.LocalPkg
	layer.reboot = ly.Reboot
	layer.ExternalBuilder = ly.ExternalBuilder
	layer.shell = ly.Shell
}
