// This plugin's OWN CUE schema — the typed plugin_input for the `dbus`
// live-container check verb, served over the Describe channel. It is the SINGLE
// SOURCE for this plugin's params, used two ways (the same contract core `spec`
// and the reference plugin-http use):
//
//  1. GENERATE the Go param struct — `cue exp gengotypes` (driven by task cue:gen,
//     which wraps this with `package params` + `@go(params)`) emits
//     ../params/cue_types_gen.go, so the provider decodes plugin_input into a TYPED
//     struct, never a hand-parsed map.
//  2. VALIDATE authored input AT RUNTIME — the host splices this source onto the
//     base (base ++ plugin) and validates every authored `dbus` step's
//     plugin_input against #DbusInput.
//
// Every dbus-EXCLUSIVE field lives here (they left core #Op in the
// schema-compaction cutover): the verb `method` enum (the former core #DbusMethod)
// plus dest/path/member/arg/text. `member` is the D-Bus MEMBER (the fully-qualified
// interface.Method name a `call` invokes) — RENAMED from the former shared `method`
// #Op modifier, so it cannot collide with the input's verb-method key. The step's
// shared modifiers stay on #Op, read off the marshalled step Op: the
// exit_status/stdout/stderr matchers (sdk.VerbVerdict) and `description` (the
// step's report label, doubling as the notify body).
//
// SELF-CONTAINED: it references NO base def, so it compiles standalone (gengotypes +
// the SDK's serve-side check) AND splices onto the base (base ++ plugin is a
// def-name collision check, not a base-reference resolver).
#DbusInput: {
	// method — the dbus verb method (the former core #DbusMethod enum).
	method: "list" | "call" | "introspect" | "notify"
	// dest — the D-Bus destination (bus name), e.g. org.freedesktop.Notifications.
	dest?: string
	// path — the D-Bus object path, e.g. /org/freedesktop/Notifications.
	path?: string
	// member — the D-Bus member: the fully-qualified interface.Method a `call`
	// invokes (e.g. org.freedesktop.Notifications.GetCapabilities). Formerly the
	// shared `method` #Op modifier; renamed to proper D-Bus terminology.
	member?: string
	// arg — typed `type:value` call arguments (string/uint32/int32/int64/uint64/
	// boolean/double), converted to GVariant text for gdbus.
	arg?: [...string] @go(Args)
	// text — the notification summary (title) for `notify`.
	text?: string
}
