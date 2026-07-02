// The BUILT-IN `unix_group` plugin's OWN CUE schema — the typed plugin_input for the
// `unix_group` verb (a getent-group probe in do:assert, a groupadd in do:act). It is the
// SINGLE SOURCE for this plugin's params, used two ways (the same contract the reference
// examplerunverb and core `spec` use):
//
//  1. GENERATE the Go param struct — `cue exp gengotypes` (driven by task cue:gen,
//     which wraps this with `package params` + `@go(params)`) emits
//     ../params/cue_types_gen.go, so the provider decodes plugin_input into a TYPED
//     struct, never a hand-parsed map.
//  2. VALIDATE authored input AT RUNTIME — the builtin serves this source over the
//     Describe channel (InProcTransport) exactly like an external serves it over
//     gRPC; the host splices it onto the base (base ++ plugin) and validates every
//     authored `unix_group` step's plugin_input against #UnixGroupInput.
//
// SELF-CONTAINED: it references NO base def, so it compiles standalone (gengotypes +
// the load-gate compile) AND splices onto the base — the base ++ plugin splice exists
// to detect a def-name collision with the base, not to resolve base refs.
//
// `unix_group` is DUAL-NATURED — the FIRST extracted state-provision verb that is BOTH a
// CheckVerbProvider (RunVerb → r.runUnixGroup, the getent-group probe that keeps the live
// *Runner) AND a ProvisionActor (RenderProvisionScript → groupadd, rendered at install
// emit AND at runtime act). `gid` was a SHARED base #Op field — the `user` verb's
// getent-passwd assertion still reads it, so gid STAYS in #Op and is reproduced standalone
// here (a bare int, NO def reference, so there is no collision when base ++ plugin
// compiles): a self-contained COPY, not a move.
#UnixGroupInput: {
	// unix_group — the group name `getent group` probes (assert) / `groupadd` creates
	// (act). The verb discriminator.
	unix_group: string @go(UnixGroup)
	// gid — the desired numeric group id. A tri-state pointer: an absent key means "any
	// gid" for the assert (no gid match required) and "let groupadd pick" for the act,
	// matching the base #Op.gid semantics the verb had before extraction.
	gid?: int & >=0 @go(GID,type=*int)
}
