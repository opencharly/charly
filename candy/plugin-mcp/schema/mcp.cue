// The `mcp` plugin's OWN CUE schema — the typed plugin_input for the `mcp`
// Model-Context-Protocol check verb. It is the SINGLE SOURCE for this plugin's
// params, used two ways (the same contract core `spec` and the http plugin use):
//
//  1. GENERATE the Go param struct — `cue exp gengotypes` (driven by the cue:gen
//     pipeline, which wraps this with `package params` + `@go(params)`) emits
//     ../params/cue_types_gen.go, so the provider decodes plugin_input into a
//     TYPED struct, never a hand-parsed map.
//  2. VALIDATE authored input AT RUNTIME — the plugin serves this source over the
//     Describe channel; the host splices it onto the base (base ++ plugin) and
//     validates every authored `mcp:` step's plugin_input against #McpInput.
//
// Since the schema-compaction cutover the per-verb fields LEFT core #Op: an
// authored `mcp: <method>` step (scalar sugar) or `mcp: {method: …, tool: …}`
// (map form) desugars to the INTERNAL plugin/plugin_input envelope, and every
// mcp-exclusive modifier lives HERE — the former core #McpMethod enum is this
// def's `method` field. `mcp_name` is read by the plugin (resolve.go's pickMcpEntry
// picks the declared mcp_provide entry it names) and is an authored mcp-step field,
// so it lives in this def for the closed input validation. The
// shared assertion matchers (exit_status/stdout/stderr) and the general
// `timeout` stay on core #Op, read off the step Op by the provider.
//
// SELF-CONTAINED: it references NO base def, so it compiles standalone
// (gengotypes + the load-gate compile) AND splices onto the base (base ++ plugin
// is a def-name collision check, not a base-reference resolver).
#McpInput: {
	// method — the mcp method to dispatch (the former core #McpMethod enum; also
	// the scalar-sugar primary: `mcp: <method>`).
	method: "ping" | "servers" | "list-tools" | "list-resources" | "list-prompts" | "call" | "read"
	// mcp_name — which declared mcp_provide server to dial when the image
	// declares several (plugin-side disambiguation; auto-picked when single).
	mcp_name?: string @go(McpName)
	// tool — the tool name `call` invokes.
	tool?: string
	// uri — the resource URI `read` reads.
	uri?: string @go(URI)
	// input — the optional JSON argument blob `call` passes to the tool.
	input?: string
}
