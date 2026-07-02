// The BUILT-IN `matching` plugin's OWN CUE schema — the typed plugin_input for the
// `matching` verb (pure in-process value matching, no target probe). It is the
// SINGLE SOURCE for this plugin's params, used two ways (the same contract the
// reference exampleprobe and core `spec` use):
//
//  1. GENERATE the Go param struct — `cue exp gengotypes` (driven by task cue:gen,
//     which wraps this with `package params` + `@go(params)`) emits
//     ../params/cue_types_gen.go, so the provider decodes plugin_input into a TYPED
//     struct, never a hand-parsed map.
//  2. VALIDATE authored input AT RUNTIME — the builtin serves this source over the
//     Describe channel (InProcTransport) exactly like an external serves it over
//     gRPC; the host splices it onto the base (base ++ plugin) and validates every
//     authored `matching` step's plugin_input against #MatchingInput.
//
// SELF-CONTAINED: it references NO base def (it reproduces the matcher shape
// standalone under plugin-private def names — #MatchingContains / #MatchingMatcher /
// #MatchingMatchOp — so there is NO collision with the base #Matcher / #MatchOpMap when
// base ++ plugin compiles), so it compiles standalone (gengotypes +
// the load-gate compile) AND splices onto the base.
#MatchingInput: {
	// matching — the value (any scalar/list/map) the matchers are evaluated against;
	// coerced to a string by the provider (sdk.MatchValueString) before matching.
	matching: _ @go(Matching)
	// contains — the goss-style matchers every authored value must satisfy. Reproduces
	// the contains matcher-list shape standalone (scalar / single operator-map / list).
	contains?: #MatchingContains @go(Contains)
}

// #MatchingContains is the contains matcher list: a single matcher OR a list.
#MatchingContains: (#MatchingMatcher | [...#MatchingMatcher])

// #MatchingMatcher mirrors the base #Matcher: a bare scalar (implicit match) or a
// single-operator map.
#MatchingMatcher: (string | bool | number | #MatchingMatchOp)

// #MatchingMatchOp mirrors the base #MatchOpMap: exactly one matcher operator key.
#MatchingMatchOp: {equals: _} | {not_equals: _} | {contains: _} | {not_contains: _} | {matches: _} | {not_matches: _} | {lt: _} | {le: _} | {gt: _} | {ge: _}
