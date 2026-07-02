package main

// provider_builder_external.go — the externalized DETECTION-builder registry surface, the
// builder-class companion of provider_deploy.go's externalizedDeploySubstrates /
// externalDeploySubstratePlugins. The four detection-builders (cargo/npm/pixi/aur) are served
// by OUT-OF-PROCESS plugin candies: their BUILD-TIME multi-stage is resolved by the plugin's
// OpResolve leg (kit.BuilderResolve, spliced by emitBuilderStages/emitBuilderArtifacts — C10),
// and their DEPLOY-TIME IR shim (per-candy stage context + teardown ops) is carried
// out-of-process over OpCollectContext + OpReverse, resolved in the host-side build PRE-PASS
// (builder_preresolve.go).
//
// Selection stays DETECTION (candyNeedsBuilder against the embedded builder: vocabulary), never an
// authored external_builder: field — so the ~39 consumer candies that carry a pixi.toml /
// package.json / Cargo.toml / aur: section are untouched. These maps only say WHICH builder words
// are external and WHICH candy serves each. Connection is PRECISELY SCOPED + on-demand: the build
// pre-pass (builder_preresolve.go) detects exactly the builders the deploy's resolved closure
// triggers (WITH the distro/build-format gate — a fedora deploy never connects aur) and connects
// only those, by the canonical ref below (the same on-demand, scoped pattern as connectPluginByWordRef,
// R3). A plugin baked into the image is unaffected; nothing surfaces builder words across an entire
// box scan.

// externalizedBuilders is THE single source of truth for which builder words are served by an
// EXTERNAL out-of-process plugin (no in-proc BuilderProvider). A word here resolves through
// providerRegistry.ResolveBuilder to a *grpcProvider connected at plugin-load time.
var externalizedBuilders = map[string]bool{
	"cargo": true,
	"npm":   true,
	"pixi":  true,
	"aur":   true,
}

// externalBuilderPlugins maps each externalized builder word to the candy SUBPATH of the plugin
// that serves it (in the default project repo) — the builder companion of
// externalDeploySubstratePlugins.
var externalBuilderPlugins = map[string]string{
	"cargo": "candy/plugin-builder-cargo",
	"npm":   "candy/plugin-builder-npm",
	"pixi":  "candy/plugin-builder-pixi",
	"aur":   "candy/plugin-builder-aur",
}

// externalBuilderPluginRef returns the canonical @github ref to the candy serving an externalized
// builder word, and whether the word is a first-party externalized builder. The build pre-pass
// pulls in the SPECIFIC detected builder's plugin by this ref (ExtraCandyRefs) when it is not
// already in the deploy's candy closure — a box/<distro> SUBMODULE deploy triggers a builder by
// detection but vendors the plugin candy nowhere (it lives in the main repo's candy/*). The SAME
// host-side-plugin pattern as externalDeploySubstratePluginRef + vmPluginCandyRef (R3); under
// CHARLY_REPO_OVERRIDE it redirects to the local superproject under development.
func externalBuilderPluginRef(word string) (string, bool) {
	sub, ok := externalBuilderPlugins[word]
	if !ok {
		return "", false
	}
	return "@" + DefaultProjectRepo + "/" + sub, true
}
