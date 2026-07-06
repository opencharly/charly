// The `vnc` plugin's OWN CUE schema — the typed plugin_input for the `vnc`
// RFB/VNC check verb. It is the SINGLE SOURCE for this plugin's params, used two
// ways (the same contract core `spec` and the http plugin use):
//
//  1. GENERATE the Go param struct — `cue exp gengotypes` (driven by the cue:gen
//     pipeline, which wraps this with `package params` + `@go(params)`) emits
//     ../params/cue_types_gen.go, so the provider decodes plugin_input into a
//     TYPED struct, never a hand-parsed map.
//  2. VALIDATE authored input AT RUNTIME — the plugin serves this source over the
//     Describe channel; the host splices it onto the base (base ++ plugin) and
//     validates every authored `vnc:` step's plugin_input against #VncInput.
//
// Since the schema-compaction cutover the per-verb fields LEFT core #Op: an
// authored `vnc: <method>` step (scalar sugar) or `vnc: {method: …, x: …}` (map
// form) desugars to the INTERNAL plugin/plugin_input envelope, and every
// vnc-exclusive modifier lives HERE — the former core #VncMethod enum is this
// def's `method` field, and the former shared #Op `method` modifier (which `rfb`
// used for the raw RFB sub-method) is RENAMED `http_method` (the input's
// `method` key is the VERB method), mirroring the cdp `raw` rename. The shared
// assertion matchers (exit_status/stdout/stderr) and the general `timeout` stay
// on core #Op, read off the step Op by the provider.
//
// SELF-CONTAINED: it references NO base def, so it compiles standalone
// (gengotypes + the load-gate compile) AND splices onto the base (base ++ plugin
// is a def-name collision check, not a base-reference resolver).
#VncInput: {
	// method — the vnc method to dispatch (the former core #VncMethod enum; also
	// the scalar-sugar primary: `vnc: <method>`).
	method: "status" | "screenshot" | "click" | "mouse" | "type" | "key" | "rfb"
	// x / y — desktop-absolute coordinates (click/mouse).
	x?: int @go(,type=int)
	y?: int @go(,type=int)
	// button — the mouse button for click (left/right/middle; default left).
	button?: string
	// text — the text `type` types.
	text?: string
	// key — the named key `key` presses.
	key?: string @go(KeyName)
	// http_method — the raw RFB sub-method `rfb` sends (key/pointer/cut-text/
	// fbupdate-request; RENAMED from the former shared #Op `method` modifier —
	// `method` here is the VERB method), mirroring the cdp `raw` rename.
	http_method?: string @go(HttpMethod)
	// params — the JSON params blob `rfb` passes to the sub-method.
	params?: string
	// artifact — the host path `screenshot` writes the PNG to.
	artifact?: string
	// artifact_min_bytes / artifact_min_dimensions / artifact_not_uniform — the
	// post-run artifact-reality assertions (sdk.RunArtifactValidators).
	artifact_min_bytes?:      int & >=0                    @go(ArtifactMinBytes,type=int)
	artifact_min_dimensions?: string & =~"^[0-9]+x[0-9]+$" @go(ArtifactMinDimensions)
	artifact_not_uniform?:    bool                         @go(ArtifactNotUniform)
}
