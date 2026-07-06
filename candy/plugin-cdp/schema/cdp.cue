// The `cdp` plugin's OWN CUE schema — the typed plugin_input for the `cdp`
// Chrome-DevTools check verb. It is the SINGLE SOURCE for this plugin's params,
// used two ways (the same contract core `spec` and the http plugin use):
//
//  1. GENERATE the Go param struct — `cue exp gengotypes` (driven by the cue:gen
//     pipeline, which wraps this with `package params` + `@go(params)`) emits
//     ../params/cue_types_gen.go, so the provider decodes plugin_input into a
//     TYPED struct, never a hand-parsed map.
//  2. VALIDATE authored input AT RUNTIME — the plugin serves this source over the
//     Describe channel; the host splices it onto the base (base ++ plugin) and
//     validates every authored `cdp:` step's plugin_input against #CdpInput.
//
// Since the schema-compaction cutover the per-verb fields LEFT core #Op: an
// authored `cdp: <method>` step (scalar sugar) or `cdp: {method: …, tab: …}`
// (map form) desugars to the INTERNAL plugin/plugin_input envelope, and every
// cdp-exclusive modifier lives HERE — the former core #CdpMethod enum is this
// def's `method` field, and the former shared #Op `method` HTTP-method modifier
// (which `raw` used for the CDP protocol method name) is RENAMED `http_method`
// (the input's `method` key is the VERB method). The shared assertion matchers
// (exit_status/stdout/stderr) and the general `timeout` stay on core #Op, read
// off the step Op by the provider.
//
// SELF-CONTAINED: it references NO base def, so it compiles standalone
// (gengotypes + the load-gate compile) AND splices onto the base (base ++ plugin
// is a def-name collision check, not a base-reference resolver).
#CdpInput: {
	// method — the cdp method to dispatch (the former core #CdpMethod enum; also
	// the scalar-sugar primary: `cdp: <method>`).
	method: "status" | "list" | "url" | "text" | "html" | "eval" | "axtree" | "coords" | "raw" | "wait" | "screenshot" | "open" | "close" | "click" | "type" | "spa-status" | "spa-click" | "spa-type" | "spa-key" | "spa-key-combo" | "spa-mouse"
	// tab — the target tab: a 1-based page index or a DevTools UUID.
	tab?: string
	// expression — the JavaScript expression `eval` evaluates.
	expression?: string
	// url — the page URL `open` navigates to.
	url?: string @go(URL)
	// selector — the CSS selector (coords/wait/click/type; pierces shadow DOM).
	selector?: string
	// text — the text `type`/`spa-type` types.
	text?: string
	// x / y — desktop-absolute coordinates (spa-click/spa-mouse).
	x?: int @go(,type=int)
	y?: int @go(,type=int)
	// button — the mouse button for spa-click (default left).
	button?: string
	// key — the named key `spa-key` presses.
	key?: string @go(KeyName)
	// combo — the key combo `spa-key-combo` sends (e.g. "ctrl+t").
	combo?: string
	// query — the optional filter `axtree` matches accessibility nodes against.
	query?: string
	// http_method — the raw CDP protocol method `raw` calls (e.g.
	// "Page.captureScreenshot"; RENAMED from the former shared #Op `method`
	// modifier — `method` here is the VERB method).
	http_method?: string @go(HttpMethod)
	// params — the JSON params blob `raw` passes to the protocol method.
	params?: string
	// artifact — the host path `screenshot` writes the PNG to.
	artifact?: string
	// artifact_min_bytes / artifact_min_dimensions / artifact_not_uniform — the
	// post-run artifact-reality assertions (sdk.RunArtifactValidators).
	artifact_min_bytes?:      int & >=0                    @go(ArtifactMinBytes,type=int)
	artifact_min_dimensions?: string & =~"^[0-9]+x[0-9]+$" @go(ArtifactMinDimensions)
	artifact_not_uniform?:    bool                         @go(ArtifactNotUniform)
}
