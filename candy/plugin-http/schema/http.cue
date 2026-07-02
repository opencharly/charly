// The BUILT-IN `http` plugin's OWN CUE schema — the typed plugin_input for the `http`
// verb (host-side request under live mode, in-container `curl` under box mode). It is
// the SINGLE SOURCE for this plugin's params, used two ways (the same contract the
// reference examplerunverb and core `spec` use):
//
//  1. GENERATE the Go param struct — `cue exp gengotypes` (driven by task cue:gen,
//     which wraps this with `package params` + `@go(params)`) emits
//     ../params/cue_types_gen.go, so the provider decodes plugin_input into a TYPED
//     struct, never a hand-parsed map.
//  2. VALIDATE authored input AT RUNTIME — the builtin serves this source over the
//     Describe channel (InProcTransport) exactly like an external serves it over
//     gRPC; the host splices it onto the base (base ++ plugin) and validates every
//     authored `http` step's plugin_input against #HttpInput.
//
// SELF-CONTAINED: it references NO base def — the matcher shape body/header reproduces
// standalone under plugin-private def names (#HttpMatcherList / #HttpMatcher /
// #HttpMatchOp, so there is NO collision with the base #MatcherList / #Matcher /
// #MatchOpMap when base ++ plugin compiles), so it compiles standalone (gengotypes +
// the load-gate compile) AND splices onto the base.
//
// Only the http-EXCLUSIVE fields live here. `method`/`request_body` are SHARED #Op
// modifiers (the live-container verbs cdp/dbus/libvirt read them too) and `timeout` is
// a GENERAL per-step modifier (the runner's probeNeverHang floor reads it for every
// verb), so all three STAY in #Op and are read off the step Op by the runner — they are
// NOT reproduced here and NOT carried in plugin_input. The provider is a
// CheckVerbProvider — it dispatches IN-PROCESS via RunVerb and so keeps the live
// *Runner (r.HTTPClient / r.Mode / r.Exec) the request needs (mirrors examplerunverb).
#HttpInput: {
	// http — the request URL (the verb discriminator).
	http: string @go(HTTP)
	// status — optional expected HTTP status code (0 = unchecked).
	status?: int & >=100 & <600 @go(,type=int)
	// body — optional goss-style matchers the response body must satisfy. Reproduces
	// the base #MatcherList shape standalone (scalar / single operator-map / list).
	body?: #HttpMatcherList @go(Body)
	// header — optional matchers the formatted response headers must satisfy.
	header?: #HttpMatcherList @go(Headers)
	// allow_insecure — skip TLS verification.
	allow_insecure?: bool @go(AllowInsecure)
	// no_follow_redirects — do not follow 3xx redirects (assert the first response).
	no_follow_redirects?: bool @go(NoFollowRedir)
	// ca_file — optional PEM CA bundle to trust for the request.
	ca_file?: string @go(CAFile)
}

// #HttpMatcherList mirrors the base #MatcherList: a single matcher OR a list.
#HttpMatcherList: (#HttpMatcher | [...#HttpMatcher])

// #HttpMatcher mirrors the base #Matcher: a bare scalar (implicit match) or a
// single-operator map.
#HttpMatcher: (string | bool | number | #HttpMatchOp)

// #HttpMatchOp mirrors the base #MatchOpMap: exactly one matcher operator key.
#HttpMatchOp: {equals: _} | {not_equals: _} | {contains: _} | {not_contains: _} | {matches: _} | {not_matches: _} | {lt: _} | {le: _} | {gt: _} | {ge: _}
