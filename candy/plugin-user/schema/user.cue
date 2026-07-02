// The BUILT-IN `user` plugin's OWN CUE schema — the typed plugin_input for the `user`
// verb (a getent-passwd probe in do:assert, a useradd in do:act). It is the SINGLE
// SOURCE for this plugin's params, used two ways (the same contract the reference
// examplerunverb and core `spec` use):
//
//  1. GENERATE the Go param struct — `cue exp gengotypes` (driven by task cue:gen,
//     which wraps this with `package params` + `@go(params)`) emits
//     ../params/cue_types_gen.go, so the provider decodes plugin_input into a TYPED
//     struct, never a hand-parsed map.
//  2. VALIDATE authored input AT RUNTIME — the builtin serves this source over the
//     Describe channel (InProcTransport) exactly like an external serves it over
//     gRPC; the host splices it onto the base (base ++ plugin) and validates every
//     authored `user` step's plugin_input against #UserInput.
//
// SELF-CONTAINED: it references NO base def, so it compiles standalone (gengotypes +
// the load-gate compile) AND splices onto the base — the base ++ plugin splice exists
// to detect a def-name collision with the base, not to resolve base refs.
//
// `user` is DUAL-NATURED — a state-provision verb that is BOTH a CheckVerbProvider
// (RunVerb → r.runUser, the getent-passwd probe that keeps the live *Runner) AND a
// ProvisionActor (RenderProvisionScript → useradd, rendered at install emit AND at
// runtime act). `uid`/`gid`/`home`/`shell` were base #Op fields read ONLY by the `user`
// verb (the `unix_group` verb that previously shared `gid` had already left #Op and
// reproduces gid in its own #UnixGroupInput), so all four MOVE here when `user` extracts
// and leave #Op entirely. The probe asserts uid/gid/home/shell when set; the act renders
// useradd with -u/-m -d/-s (gid is not set by the current act form — it is decoded for
// the assert only).
#UserInput: {
	// user — the account name `getent passwd` probes (assert) / `useradd` creates (act).
	// The verb discriminator.
	user: string @go(User)
	// uid — optional expected numeric user id (assert) / `-u` flag (act). Tri-state pointer.
	uid?: int & >=0 @go(UID,type=*int)
	// gid — optional expected numeric primary group id (assert only). Tri-state pointer.
	gid?: int & >=0 @go(GID,type=*int)
	// home — optional expected home dir (assert) / `-m -d` flag (act).
	home?: string
	// shell — optional expected login shell (assert) / `-s` flag (act).
	shell?: string
}
