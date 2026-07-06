// This out-of-tree plugin's OWN CUE schema, served over the Describe channel — the
// typed plugin_input for the `appium` W3C-WebDriver check verb. It is the SINGLE
// SOURCE for this plugin's params, used two ways (the same contract core `spec` and
// the http plugin use):
//
//  1. GENERATE the Go param struct — `cue exp gengotypes` (driven by task cue:gen,
//     which wraps this with `package params` + `@go(params)`) emits
//     ../params/cue_types_gen.go, so the provider decodes plugin_input into a TYPED
//     struct, never a hand-parsed map.
//  2. VALIDATE authored input AT RUNTIME — the plugin serves this source over the
//     Describe channel; the host splices it onto the base (base ++ plugin) and
//     validates every authored `appium:` step's plugin_input against #AppiumInput.
//
// Since the schema-compaction cutover the per-verb fields left core #Op: a step's
// `appium: <method>` sugar desugars to the internal plugin/plugin_input pair, the
// method name rides the input's `method` key (the former core #AppiumMethod enum),
// and every appium-exclusive modifier lives HERE — including `caps` (the core #Op
// caps field is setcap's modifier only now) and `http_method` (RENAMED from the
// former shared #Op `method` request modifier the raw method read; the input's
// `method` key is the VERB method). Only the genuinely SHARED step modifiers
// (timeout, the exit_status/stdout/stderr matchers, context, …) stay on core #Op,
// read off the step Op by the provider.
//
// SELF-CONTAINED: it references NO base def, so it compiles standalone (the SDK's
// serve-side check + gengotypes) AND splices onto the base (base ++ plugin is a
// def-name collision check, not a base-reference resolver).

// #AppiumInput is the `appium` verb's plugin_input: the method name plus its
// method-exclusive modifiers.
#AppiumInput: {
	// method — the appium method name (the former core #AppiumMethod enum; the
	// verb's PRIMARY input field, so `appium: status` desugars to {method: "status"}).
	method: ("status" | "session-create" | "session-delete" | "install-app" | "find" | "click" | "send-keys" | "screenshot" | "get-text" | "get-attribute" | "clear" | "find-all" | "source" | "back" | "gesture-tap" | "gesture-double-tap" | "gesture-long-press" | "gesture-drag" | "gesture-swipe" | "gesture-scroll" | "gesture-fling" | "gesture-pinch-open" | "gesture-pinch-close" | "app-start-activity" | "app-activate" | "app-terminate" | "app-remove" | "app-clear" | "app-is-installed" | "app-state" | "app-current-activity" | "app-current-package" | "key-press" | "key-hide" | "key-shown" | "device-info" | "device-battery" | "device-time" | "device-orientation" | "device-set-orientation" | "device-notifications" | "device-get-clipboard" | "device-set-clipboard" | "device-contexts" | "device-context" | "execute" | "raw") @go(Method,type=string)
	// caps — the W3C capabilities JSON (session-create; @path reads a file).
	caps?: string
	// session — an explicit session id overriding the persisted session file.
	session?: string
	// strategy / selector — the element locator (find/click/send-keys/…).
	strategy?: string
	selector?: string
	// text — the keys to send (send-keys).
	text?: string
	// attribute — the element attribute name (get-attribute).
	attribute?: string
	// apk — HOST path of the APK to stage + install (install-app); resolved to an
	// absolute candy-anchored path by the host before marshaling.
	apk?: string
	// app_id — the application package id (app-activate/terminate/remove/…).
	app_id?: string @go(AppId)
	// activity — the start-activity intent (`pkg/.activity`).
	activity?: string
	// keycode — the Android keycode (key-press).
	keycode?: int @go(,type=int)
	// params — free-form JSON args merged into the mobile: command
	// (gestures/app/key/device groups), or the scalar value for
	// device-set-orientation / device-set-clipboard / device-context.
	params?: string
	// x / y — coordinate target for a gesture with no selector.
	x?: int @go(,type=int)
	y?: int @go(,type=int)
	// direction / percent — swipe/scroll/fling geometry.
	direction?: string
	percent?:   string
	// expression — the execute method's script (`mobile: <cmd>` or JS).
	expression?: string
	// http_method / path / request_body — the raw escape hatch: the W3C HTTP verb
	// (http_method — renamed from the former shared #Op `method`), the
	// /session/<id>-relative path, and the JSON body ({element} substitution when
	// selector is set; execute reads request_body too).
	http_method?:  string @go(HTTPMethod)
	path?:         string
	request_body?: string @go(RequestBody)
	// artifact + validators — screenshot's PNG output path and the post-run
	// artifact-reality assertions (sdk.RunArtifactValidators reads them off the input).
	artifact?:                 string
	artifact_min_bytes?:       int & >=0                    @go(ArtifactMinBytes,type=int)
	artifact_min_dimensions?:  string & =~"^[0-9]+x[0-9]+$" @go(ArtifactMinDimensions)
	artifact_not_uniform?:     bool                         @go(ArtifactNotUniform)
	artifact_min_cast_events?: int & >=0                    @go(ArtifactMinCastEvents,type=int)
}
