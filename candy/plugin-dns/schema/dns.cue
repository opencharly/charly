// The BUILT-IN `dns` plugin's OWN CUE schema — the typed plugin_input for the `dns`
// verb (host-side `net.LookupIP` under live mode, in-container `getent hosts` under
// box mode). It is the SINGLE SOURCE for this plugin's params, used two ways (the same
// contract the reference examplerunverb and core `spec` use):
//
//  1. GENERATE the Go param struct — `cue exp gengotypes` (driven by task cue:gen,
//     which wraps this with `package params` + `@go(params)`) emits
//     ../params/cue_types_gen.go, so the provider decodes plugin_input into a TYPED
//     struct, never a hand-parsed map.
//  2. VALIDATE authored input AT RUNTIME — the builtin serves this source over the
//     Describe channel (InProcTransport) exactly like an external serves it over
//     gRPC; the host splices it onto the base (base ++ plugin) and validates every
//     authored `dns` step's plugin_input against #DnsInput.
//
// SELF-CONTAINED: it references NO base def, so it compiles standalone (gengotypes +
// the load-gate compile) AND splices onto the base — the base ++ plugin splice exists
// to detect a def-name collision with the base, not to resolve base refs.
//
// `addrs` is a SHARED base #Op field (the `interface` verb also reads it), so it STAYS
// in #Op; this plugin reproduces its `[...string]` shape standalone here (a bare
// primitive list, NO def reference, so there is no collision when base ++ plugin
// compiles). `server` is the dns verb's own (currently advisory) authoring field,
// preserved here verbatim so an authored `dns:`+`server:` step still validates. The
// (distinct) box/deploy `dns:` HOSTNAME field is a SEPARATE schema element
// (BoxMetadata.DNS) and is unaffected. The provider is a CheckVerbProvider — it
// dispatches IN-PROCESS via RunVerb (mirrors examplerunverb).
#DnsInput: {
	// dns — the hostname to resolve (the verb discriminator).
	dns: string @go(DNS)
	// resolvable — whether the hostname is expected to resolve (default true). A
	// tri-state pointer so an absent key means "expected resolvable".
	resolvable?: bool @go(Resolvable,type=*bool)
	// addrs — optional required resolved addresses; a resolved IP must match one.
	addrs?: [...string] @go(Addrs)
	// server — optional advisory resolver hint (preserved for authoring compatibility).
	server?: string @go(Server)
}
