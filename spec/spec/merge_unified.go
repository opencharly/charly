package spec

import "encoding/json"

// merge_unified.go — the kind-blind document MERGE half of the unified-config loader.
// These are pure map/struct merges over an already-parsed UnifiedFile: no provider
// registry, no plugin round-trip, no charly-core helper. The host materialize
// (charly/materialize.go) and loaderkit's MaterializeLoadedProject replay the walk's
// documents in order — the root file first, then its flat imports — calling MergeUnified
// for each, so the root file's values are present before any import's fields are considered
// (root-wins). Relocated from sdk/loaderkit/merge.go to the dedicated spec module (#55 C3b)
// alongside its siblings MergePluginKindsMap (merge_plugin_kinds.go) and AnchorScanSpecs
// (load_directives.go), so charly core reaches it WITHOUT importing loaderkit — the same
// import-purity route MaterializeProjectSeams and MergePluginKindsMap already took.

// MergeUnified merges src into dst such that dst's existing values WIN on
// conflict at the same leaf (root-wins). This means when MaterializeLoadedProject
// replays the walk's documents in order (the root file first, then its flat
// imports), the root file's values are already present before any import's
// fields are considered, so root wins.
//
// For included files: the same MergeUnified is called but dst already contains
// the root's values, so those fields stay untouched. src's fields that aren't
// present in dst get copied over. That's the desired semantics.
func MergeUnified(dst, src *UnifiedFile, srcDir string) {
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
		dst.Discover = append(dst.Discover, AnchorScanSpecs(src.Discover, srcDir)...)
	}
	mergeRawTemplateMap(&dst.Box, src.Box)
	mergeRawTemplateMap(&dst.Candy, src.Candy)
	// PluginKinds carries every plugin-extracted kind — the build vocabulary
	// (distro/builder/init/resource), the Calamares target, sidecar/agent/module/
	// package-group, AND (K1 unit-1 follow-up) the 5 standalone-substrate-TEMPLATE kinds
	// vm/pod/kubernetes/local/android (formerly 5 separate mergeRawTemplateMap calls into dedicated
	// fields — now subsumed here too, since they fold into PluginKinds[disc][name] like every
	// other templated kind) — merged once here (root-wins, name-keyed override). The former
	// mergeDistroMap/mergeBuilderMap/mergeInitMap/mergeResourceMap/mergeTargetMap calls
	// are subsumed by this one generic merge.
	MergePluginKindsMap(&dst.PluginKinds, src.PluginKinds)
	mergeDeployMaps(&dst.Fleet, src.Fleet)
	if dst.Provides == nil && src.Provides != nil {
		dst.Provides = src.Provides
	}
	// Defaults: dst wins per-field if set.
	mergeBoxConfig(&dst.Defaults, &src.Defaults)
}

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

// mergeDeployMaps merges src into dst, dst-wins on name collisions.
// Field-singular cutover: replaces the legacy mergeDeployments which
// took *DeploymentsSection wrappers. Provides now lives at UnifiedFile
// root and is merged separately by MergeUnified.
func mergeDeployMaps(dst *map[string]FleetNode, src map[string]FleetNode) {
	if len(src) == 0 {
		return
	}
	if *dst == nil {
		*dst = make(map[string]FleetNode)
	}
	for k, v := range src {
		if _, exists := (*dst)[k]; !exists {
			(*dst)[k] = v
		}
	}
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
