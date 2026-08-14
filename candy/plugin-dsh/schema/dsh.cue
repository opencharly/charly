// This compiled-in plugin's OWN CUE schema, served over the Describe channel — the
// typed plugin_input for the `dsh` check verb. It is the SINGLE SOURCE for this
// plugin's verb params, used two ways (the same contract core `spec` and the http
// plugin use):
//
//  1. GENERATE the Go param struct — `cue exp gengotypes` (driven by task cue:gen,
//     which wraps this with `package params` + `@go(params)`) emits
//     ../params/cue_types_gen.go, so the provider decodes plugin_input into a TYPED
//     struct, never a hand-parsed map.
//  2. VALIDATE authored input AT RUNTIME — the plugin serves this source over the
//     Describe channel; the host splices it onto the base (base ++ plugin) and
//     validates every authored `dsh:` step's plugin_input against #DshInput.
//
// The verb is IN-BOX (the one place plugin-dsh diverges from plugin-agentteams'
// host-based cc.ResolveEndpoint): the provider runs dsh commands INSIDE the venue
// via cc.Exec().RunCapture — `dsh --version`, `ls $DSH_HOME/profiles`, `dsh plugin
// --profile <name> list` — and probes the web UI on 127.0.0.1:3080 in-box (the
// loopback-bound web app is unreachable from the host except through the socat
// forwarder, and the in-box probe is the direct check). Only the genuinely SHARED
// step modifiers (timeout, the exit_status/stdout/stderr matchers, context, …) stay
// on core #Op, read off the step Op by the provider.
//
// SELF-CONTAINED: it references NO base def, so it compiles standalone (the SDK's
// serve-side check + gengotypes) AND splices onto the base (base ++ plugin is a
// def-name collision check, not a base-reference resolver).

// #DshInput is the `dsh` verb's plugin_input: the method name plus its
// method-exclusive modifiers.
#DshInput: {
	// method — the dsh verb method name (the verb's PRIMARY input field, so
	// `dsh: version` desugars to {method: "version"}).
	method: ("version" | "web-running" | "profile-list" | "plugin-list") @go(Method,type=string)
	// profile — the profile name for plugin-list (default "web", the profile the
	// dsh candy's service boots).
	profile?: string
}
