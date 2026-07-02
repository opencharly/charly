// schema/candy.cue — the SELF-CONTAINED CUE schema candy/plugin-candy-kind ships over Describe
// (schema_cue). References NO base def so it compiles standalone (BuildCapabilities compiles it
// alone, failing loudly if broken) AND splices onto the base (the base ++ plugin splice detects a
// def-name collision — hence a UNIQUE name, never #Candy/#Box/#CandyValue which already exist in
// the base).
//
// UNLIKE most plugins, this schema does NOT validate a per-word plugin_input: the `candy` kind is
// the box⊻layer FACTORY whose value is RICH + core-referencing (#Candy/#Box with
// host-canonicalized shorthand) and CANNOT be a self-contained plugin def. So the capability
// declares InputDef:"" and the HOST validates the authored value against the KEPT #CandyValue core
// def (runPluginKind → validateKindValueCUE). This def exists ONLY to satisfy the non-empty-schema
// load gate and to DOCUMENT the seam — it is never used for validation.
//
// candy/plugin-candy-kind is a PURE ECHO: the host pre-decodes the CANONICAL box⊻layer node via
// the BOOTSTRAP-CRITICAL core candyIsImage + buildCandy (which STAY core — the discovered-candy
// pre-check calls them directly), validates it against #CandyValue, and threads the result in
// op.Env (spec.StructuralKindLoadEnv.Standalone: Shape "candy-image" → spec.Box, "candy-layer" →
// spec.Candy); the plugin returns it; the host folds into uf.Box (image) or uf.Candy (layer). The
// COMPILED-IN placement means there is NO bootstrap cycle (registered at init, before any load).
#CandyKindLoad: {
	// shape names the fold the host performs on the echo (informational — the host owns it).
	shape?: "candy-image" | "candy-layer"
}
