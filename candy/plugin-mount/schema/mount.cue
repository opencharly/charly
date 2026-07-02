// The BUILT-IN `mount` plugin's OWN CUE schema — the typed plugin_input for the `mount`
// verb (a findmnt probe in do:assert, a `mount` in do:act). It is the SINGLE SOURCE for
// this plugin's params, used two ways (the same contract the reference examplerunverb
// and core `spec` use):
//
//  1. GENERATE the Go param struct — `cue exp gengotypes` (driven by task cue:gen,
//     which wraps this with `package params` + `@go(params)`) emits
//     ../params/cue_types_gen.go, so the provider decodes plugin_input into a TYPED
//     struct, never a hand-parsed map.
//  2. VALIDATE authored input AT RUNTIME — the builtin serves this source over the
//     Describe channel (InProcTransport) exactly like an external serves it over
//     gRPC; the host splices it onto the base (base ++ plugin) and validates every
//     authored `mount` step's plugin_input against #MountInput.
//
// SELF-CONTAINED: it references NO base def — the matcher shape `opt` reproduces
// standalone under plugin-private def names (#MountMatcherList / #MountMatcher /
// #MountMatchOp, so there is NO collision with the base #MatcherList / #Matcher /
// #MatchOpMap when base ++ plugin compiles) — so it compiles standalone (gengotypes +
// the load-gate compile) AND splices onto the base.
//
// `mount` is DUAL-NATURED — a state-provision verb that is BOTH a CheckVerbProvider
// (RunVerb → r.runMount, the findmnt probe that keeps the live *Runner) AND a
// ProvisionActor (RenderProvisionScript → mount, rendered at install emit AND at runtime
// act). `mount_source`/`filesystem`/`opt` were base #Op fields read ONLY by the `mount`
// verb, so all three MOVE here when `mount` extracts and leave #Op entirely.
#MountInput: {
	// mount — the mountpoint `findmnt` probes (assert) / `mount` targets (act). The verb
	// discriminator.
	mount: string @go(Mount)
	// mount_source — optional expected source device (assert) / the source argument (act).
	mount_source?: string @go(MountSource)
	// filesystem — optional expected fstype (assert) / `-t` flag (act).
	filesystem?: string
	// opt — optional goss-style matchers the mount options must satisfy (assert) / `-o`
	// flag value (act). Reproduces the base #MatcherList shape standalone (scalar / single
	// operator-map / list).
	opt?: #MountMatcherList @go(Opts)
}

// #MountMatcherList mirrors the base #MatcherList: a single matcher OR a list.
#MountMatcherList: (#MountMatcher | [...#MountMatcher])

// #MountMatcher mirrors the base #Matcher: a bare scalar (implicit match) or a
// single-operator map.
#MountMatcher: (string | bool | number | #MountMatchOp)

// #MountMatchOp mirrors the base #MatchOpMap: exactly one matcher operator key.
#MountMatchOp: {equals: _} | {not_equals: _} | {contains: _} | {not_contains: _} | {matches: _} | {not_matches: _} | {lt: _} | {le: _} | {gt: _} | {ge: _}
