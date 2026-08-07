// The BUILT-IN `command` plugin's OWN CUE schema — the typed plugin_input for the
// `command` verb (run a shell command in-container/host and assert exit/stdout/stderr).
// It is the SINGLE SOURCE for this plugin's params, used two ways (the same contract
// the reference examplerunverb and core `spec` use):
//
//  1. GENERATE the Go param struct — `cue exp gengotypes` (driven by task cue:gen,
//     which wraps this with `package params` + `@go(params)`) emits
//     ../params/cue_types_gen.go, so the provider decodes plugin_input into a TYPED
//     struct, never a hand-parsed map.
//  2. VALIDATE authored input AT RUNTIME — the builtin serves this source over the
//     Describe channel (InProcTransport) exactly like an external serves it over
//     gRPC; the host splices it onto the base (base ++ plugin) and validates every
//     authored `command` step's plugin_input against #CommandInput.
//
// SELF-CONTAINED: every field is a bare primitive referencing NO base def, so it
// compiles standalone (gengotypes + the load-gate compile) AND splices onto the base
// — the base ++ plugin splice exists to detect a def-name collision with the base,
// not to resolve base refs.
//
// `command` is the HARDEST extracted verb: its ACT is the full install-task emitCmd
// path (a `run: plugin: command` step renders a Containerfile RUN at build /
// renderOpCommand body at deploy — NOT a RenderProvisionScript), so the provider is a
// CheckVerbProvider only (NOT a ProvisionActor); the act-emit is the dedicated
// `plugin == "command"` branch in emitTasks/renderOpCommand/opActsInBuildDeploy.
//
// FIELD SPLIT: ONLY the command-EXCLUSIVE fields live here. The matchers
// exit_status/stdout/stderr (shared with the live-container verbs via matchAll) and
// the general modifiers timeout/method/request_body/env STAY in #Op and are read off
// the step Op by the runner — they are NOT reproduced here and NOT carried in
// plugin_input. `command` itself ALSO stays an #Op modifier (wl/libvirt verbs read it,
// the act-emit rehydrates it) but is reproduced here as the plugin's discriminator.
#CommandInput: {
	// command — the shell command to run (the verb discriminator; multi-line OK).
	command: string & !="" @go(Command)
	// in_container — run via `podman exec` (default true) or host-side `sh -c` (false).
	// A tri-state pointer so an absent key means "in container", matching the base
	// #Op.in_container semantics the verb had before extraction.
	in_container?: bool @go(InContainer,type=*bool)
	// background — host-side fire-and-forget; plan teardown reaps via SIGTERM.
	background?: bool @go(Background)
	// from_host — force host-side execution (equivalent to in_container: false).
	from_host?: bool @go(FromHost)
	// expect_non_zero — assert the command FAILS (exit code != 0), whatever the
	// exact code. The base #Op exit_status matcher can only assert ONE specific
	// code (exit_status: 1); a command whose failure code is non-deterministic
	// (127 vs 1 vs 2) or simply "must fail" cannot be expressed that way. When
	// true, the runner asserts exitCode != 0 and IGNORES exit_status (the two are
	// mutually-exclusive intents: exact-code vs any-non-zero). Default false =
	// the historic behaviour (assert exit_status, defaulting to 0).
	expect_non_zero?: bool @go(ExpectNonZero)
}
