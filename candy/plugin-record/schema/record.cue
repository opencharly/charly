// The `record` plugin's OWN CUE schema — the typed plugin_input for the
// `record` session-recording check verb. It is the SINGLE SOURCE for this
// plugin's params, used two ways (the same contract core `spec` and the http
// plugin use):
//
//  1. GENERATE the Go param struct — `cue exp gengotypes` (driven by the cue:gen
//     pipeline, which wraps this with `package params` + `@go(params)`) emits
//     ../params/cue_types_gen.go, so the provider decodes plugin_input into a
//     TYPED struct, never a hand-parsed map.
//  2. VALIDATE authored input AT RUNTIME — the plugin serves this source over the
//     Describe channel; the host splices it onto the base (base ++ plugin) and
//     validates every authored `record:` step's plugin_input against
//     #RecordInput.
//
// Since the schema-compaction cutover the per-verb fields LEFT core #Op: an
// authored `record: <method>` step (scalar sugar) or `record: {method: …,
// record_name: …}` (map form) desugars to the INTERNAL plugin/plugin_input
// envelope, and every record-exclusive modifier lives HERE — the former core
// #RecordMethod enum is this def's `method` field. The shared assertion
// matchers (exit_status/stdout/stderr) and the general `timeout` stay on core
// #Op, read off the step Op by the provider.
//
// SELF-CONTAINED: it references NO base def, so it compiles standalone
// (gengotypes + the load-gate compile) AND splices onto the base (base ++ plugin
// is a def-name collision check, not a base-reference resolver).
#RecordInput: {
	// method — the record method to dispatch (the former core #RecordMethod
	// enum; also the scalar-sugar primary: `record: <method>`).
	method: "list" | "start" | "stop" | "cmd"
	// record_name — the recording session name (default "default").
	record_name?: string @go(RecordName)
	// record_mode — terminal (asciinema) / desktop (pixelflux-record or
	// wf-recorder); empty means auto-detect from the venue's tools.
	record_mode?: string @go(RecordMode)
	// record_fps — the desktop-recorder frame rate (default 30).
	record_fps?: int & >=0 @go(RecordFps,type=int)
	// record_audio — capture audio with the desktop recording.
	record_audio?: bool @go(RecordAudio)
	// text — the command line `cmd` sends into the recording's tmux session.
	text?: string
	// artifact — the host path `stop` copies the recording to.
	artifact?: string
	// artifact_min_bytes / artifact_min_cast_events — the post-run
	// artifact-reality assertions (sdk.RunArtifactValidators).
	artifact_min_bytes?:       int & >=0 @go(ArtifactMinBytes,type=int)
	artifact_min_cast_events?: int & >=0 @go(ArtifactMinCastEvents,type=int)
}
