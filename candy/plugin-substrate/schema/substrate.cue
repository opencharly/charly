// schema/substrate.cue — the SELF-CONTAINED CUE schema candy/plugin-substrate ships over
// Describe (schema_cue). References NO base def so it compiles standalone (BuildCapabilities
// compiles it alone, failing loudly if broken) AND splices onto the base (the base ++ plugin
// splice detects a def-name collision — hence a UNIQUE name, never #Pod/#Vm/#Deploy which
// already exist in the base).
//
// UNLIKE every other plugin, this schema does NOT validate a per-word plugin_input: the 5
// substrate kinds (pod/vm/k8s/local/android) have a RICH, core-referencing value
// (#Vm/#Deploy/#LibvirtDomain/… with host-canonicalized shorthand) that CANNOT be expressed
// as a self-contained plugin def. So each capability declares InputDef:"" and the HOST
// validates the authored value against the KEPT #<Kind>Value core defs
// (runPluginKind → validateStandaloneKindValueCUE). This def exists ONLY to satisfy the
// non-empty-schema load gate and to DOCUMENT the seam — it is never used for validation.
//
// The substrate provider is a PURE ECHO: the host pre-decodes the CANONICAL node (deploy
// BundleNode or per-substrate template) via the core loader and threads it in op.Env
// (spec.StructuralKindLoadEnv.Standalone); the plugin returns it; the host folds it into
// uf.Bundle (deploy) or the typed template map uf.Pod/uf.VM/… (template). See plugin.go.
#SubstrateKindLoad: {
	// shape names the fold the host performs on the echo (informational — the host owns it).
	shape?: "deploy" | "template"
}
