// This out-of-tree plugin's OWN CUE schema — the typed plugin_input for the verbs
// it provides. It is the SINGLE SOURCE for this plugin's params, used two ways (the
// same contract the built-in exampleprobe and core `spec` use):
//
//  1. GENERATE the Go param struct — `cue exp gengotypes` (driven by task cue:gen,
//     which wraps this with `package params` + `@go(params)`) emits
//     params/cue_types_gen.go, so the plugin decodes plugin_input into a TYPED
//     struct, never a hand-parsed map[string]any.
//  2. VALIDATE authored input AT RUNTIME — the plugin SERVES this source over the
//     Describe channel (the gRPC schema_cue field via sdk.BuildCapabilities); the
//     host splices it onto its base (base ++ plugin) and validates every authored
//     `externalprobe` step's plugin_input against #ExternalprobeInput. The host
//     NEVER reads this file from disk — the schema travels with the plugin.
//
// SELF-CONTAINED (carries no package clause and references NO base def): it
// compiles standalone (gengotypes + the SDK serve-side check) AND splices onto the
// base — the base ++ plugin splice exists to detect a def-name collision with the
// base, not to resolve base references. CUE stays the single source: no
// hand-written struct, no hand-validation.

// #ExternalprobeInput — the plugin_input shape for the `externalprobe` verb.
#ExternalprobeInput: {
	// marker is a NON-EMPTY string the plugin echoes back as the check result
	// message (proving the value round-trips author -> wire -> external process
	// -> result). The `& !=""` constraint is enforced by the host at validate time.
	marker: string & !="" @go(Marker)
}
