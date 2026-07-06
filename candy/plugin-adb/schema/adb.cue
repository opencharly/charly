// This out-of-tree plugin's OWN CUE schema, served over the Describe channel — the
// typed plugin_input for the `adb` Android-Debug-Bridge check verb. It is the SINGLE
// SOURCE for this plugin's params, used two ways (the same contract core `spec` and
// the http plugin use):
//
//  1. GENERATE the Go param struct — `cue exp gengotypes` (driven by task cue:gen,
//     which wraps this with `package params` + `@go(params)`) emits
//     ../params/cue_types_gen.go, so the provider decodes plugin_input into a TYPED
//     struct, never a hand-parsed map.
//  2. VALIDATE authored input AT RUNTIME — the plugin serves this source over the
//     Describe channel; the host splices it onto the base (base ++ plugin) and
//     validates every authored `adb:` step's plugin_input against #AdbInput.
//
// Since the schema-compaction cutover the per-verb fields left core #Op: a step's
// `adb: <method>` sugar desugars to the internal plugin/plugin_input pair, the method
// name rides the input's `method` key (the former core #AdbMethod enum), and every
// adb-exclusive modifier (arg/apk/app_id/source/arch/app_version/property/key/query/
// amount/artifact + the artifact validators) lives HERE. Only the genuinely SHARED
// step modifiers (timeout, the exit_status/stdout/stderr matchers, context, …) stay
// on core #Op, read off the step Op by the provider.
//
// SELF-CONTAINED: it references NO base def, so it compiles standalone (the SDK's
// serve-side check + gengotypes) AND splices onto the base (base ++ plugin is a
// def-name collision check, not a base-reference resolver).
//
// The plugin ALSO serves deploy:android (the `target: android` substrate) — that
// capability keeps its authoring contract on core #Android / the apk: format and
// carries NO plugin_input, so no input def for it lives here.

// #AdbInput is the `adb` verb's plugin_input: the method name plus its
// method-exclusive modifiers.
#AdbInput: {
	// method — the adb method name (the former core #AdbMethod enum; the verb's
	// PRIMARY input field, so `adb: devices` desugars to {method: "devices"}).
	method: ("devices" | "shell" | "install" | "install-app" | "uninstall" | "getprop" | "screencap" | "logcat-tail" | "wait-for-device" | "wait-ui-settled" | "current-focus" | "keyevent") @go(Method,type=string)
	// arg — the shell argv (shell: arg[0] program + arg[1:] args) / the package id
	// (uninstall: arg[0]).
	arg?: [...string] @go(Args)
	// apk — HOST path of a committed APK (install); resolved to an absolute
	// candy-anchored path by the host before marshaling.
	apk?: string
	// app_id / source / arch / app_version — the apkeep package spec (install-app).
	app_id?:      string @go(AppId)
	source?:      string
	arch?:        string
	app_version?: string @go(AppVersion)
	// property — the system property key (getprop), e.g. sys.boot_completed.
	property?: string
	// key — the key event to send (keyevent), e.g. KEYCODE_HOME or a numeric code.
	key?: string @go(KeyName)
	// query / amount — the literal logcat filter spec + last-N-lines trim (logcat-tail).
	query?:  string
	amount?: int @go(,type=int)
	// artifact + validators — screencap's PNG output path and the post-run
	// artifact-reality assertions (sdk.RunArtifactValidators reads them off the input).
	artifact?:                 string
	artifact_min_bytes?:       int & >=0                    @go(ArtifactMinBytes,type=int)
	artifact_min_dimensions?:  string & =~"^[0-9]+x[0-9]+$" @go(ArtifactMinDimensions)
	artifact_not_uniform?:     bool                         @go(ArtifactNotUniform)
	artifact_min_cast_events?: int & >=0                    @go(ArtifactMinCastEvents,type=int)
}
