// The reference BUILT-IN plugin's OWN CUE schema — the typed plugin_input for the
// `exampleprobe` verb. It is the SINGLE SOURCE for this plugin's params, used two
// ways (the same contract an external plugin and core `spec` use):
//
//  1. GENERATE the Go param struct — `cue exp gengotypes` (driven by task cue:gen,
//     which wraps this with `package params` + `@go(params)`) emits
//     ../params/cue_types_gen.go, so the provider decodes plugin_input into a TYPED
//     struct, never a hand-parsed map.
//  2. VALIDATE authored input AT RUNTIME — the builtin serves this source over the
//     Describe channel (InProcTransport) exactly like an external serves it over
//     gRPC; the host splices it onto the base (base ++ plugin) and validates every
//     authored `exampleprobe` step's plugin_input against #ExampleprobeInput.
//
// SELF-CONTAINED: it references NO base def, so it compiles standalone (gengotypes +
// the load-gate compile) AND splices onto the base — the base ++ plugin splice
// exists to detect a def-name collision with the base, not to resolve base refs.
#ExampleprobeInput: {
	// marker — a NON-EMPTY string the provider echoes back as the result message,
	// proving the value round-trips author -> provider -> result. The host enforces
	// `& !=""` at validate time, so an authored empty / missing marker is a hard error.
	marker: string & !="" @go(Marker)
}
