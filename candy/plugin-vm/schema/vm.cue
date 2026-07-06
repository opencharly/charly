// This out-of-tree VM plugin's OWN CUE schema, served over the Describe channel — the
// typed plugin_input for the `libvirt` VM-probe check verb. It is the SINGLE SOURCE
// for this plugin's params, used two ways (the same contract core `spec` and the http
// plugin use):
//
//  1. GENERATE the Go param struct — `cue exp gengotypes` (driven by task cue:gen,
//     which wraps this with `package params` + `@go(params)`) emits
//     ../params/cue_types_gen.go, so the provider decodes plugin_input into a TYPED
//     struct, never a hand-parsed map.
//  2. VALIDATE authored input AT RUNTIME — the plugin serves this source over the
//     Describe channel; the host splices it onto the base (base ++ plugin) and
//     validates every authored `libvirt:` step's plugin_input against #LibvirtVerbInput.
//
// Since the schema-compaction cutover the per-verb fields left core #Op: a step's
// `libvirt: <method>` sugar desugars to the internal plugin/plugin_input pair, the
// method name rides the input's `method` key (the former core #LibvirtMethod enum),
// and every libvirt-exclusive modifier lives HERE — including `command` (the
// guest/exec argv, ABSORBED from the former shared #Op command modifier). The
// snapshot methods keep requiring the step-level `target:` field — a genuinely
// SHARED #Op modifier that stays on the op, like timeout and the
// exit_status/stdout/stderr matchers.
//
// SELF-CONTAINED: it references NO base def, so it compiles standalone (the SDK's
// serve-side check + gengotypes) AND splices onto the base (base ++ plugin is a
// def-name collision check, not a base-reference resolver).
//
// The plugin ALSO serves command:vm (`charly vm …`, the externalized VM lifecycle
// CLI) — the command parses its own args out-of-process and carries NO plugin_input,
// so no input def for it lives here.

// #LibvirtVerbInput is the `libvirt` verb's plugin_input: the method name plus its
// method-exclusive modifiers.
#LibvirtVerbInput: {
	// method — the libvirt method name (the former core #LibvirtMethod enum; the
	// verb's PRIMARY input field, so `libvirt: info` desugars to {method: "info"}).
	method: ("list" | "info" | "screenshot" | "send-key" | "passwd" | "qmp" | "domain-xml" | "console" | "events" | "guest/ping" | "guest/info" | "guest/os-info" | "guest/time" | "guest/hostname" | "guest/users" | "guest/interfaces" | "guest/disks" | "guest/fsinfo" | "guest/vcpus" | "guest/exec" | "guest/fstrim" | "snapshot/list" | "snapshot/create" | "snapshot/info" | "snapshot/revert" | "snapshot/delete") @go(Method,type=string)
	// text — passwd's new graphics password / qmp's command name.
	text?: string
	// input — qmp's optional JSON args blob.
	input?: string
	// key — send-key's key/chord spec (whitespace-split into keycode slots).
	key?: string @go(KeyName)
	// command — guest/exec's argv (whitespace-split; absorbed from the former
	// shared #Op command modifier — authored `command:` on a step is the command
	// plugin's sugar key now).
	command?: string
	// uri — libvirt connection URI override ("" → qemu:///session; the nested CLI
	// also honours CHARLY_LIBVIRT_URI).
	uri?: string @go(URI)
	// artifact + validators — screenshot's PNG output path and the post-run
	// artifact-reality assertions (sdk.RunArtifactValidators reads them off the input).
	artifact?:                 string
	artifact_min_bytes?:       int & >=0                    @go(ArtifactMinBytes,type=int)
	artifact_min_dimensions?:  string & =~"^[0-9]+x[0-9]+$" @go(ArtifactMinDimensions)
	artifact_not_uniform?:     bool                         @go(ArtifactNotUniform)
	artifact_min_cast_events?: int & >=0                    @go(ArtifactMinCastEvents,type=int)
}
