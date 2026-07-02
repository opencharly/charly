// This out-of-tree plugin's OWN CUE schema, served over the Describe channel.
//
// mcp is an EXTERNAL-CHARLY-VERB plugin: like the built-in cdp/vnc/spice verbs, it
// KEEPS its `mcp:` discriminator + every modifier (mcp_name/tool/uri/input/timeout)
// on charly's core closed #Op — authoring is UNCHANGED (`mcp: ping`, not
// `plugin: mcp`). The method-name vocabulary (#McpMethod) and the modifiers therefore
// live on core #Op, NOT here, so this plugin advertises a verb with NO plugin_input
// and NO input def. The host dispatches it through the registry exactly like a
// built-in (ResolveVerb("mcp") → the out-of-process grpcProvider → invokeVerbProvider
// hands it the FULL #Op as params_json, after pre-resolving the deployment's declared
// mcp_provides + the picked, host-routable dial endpoint host-side — the plugin needs
// no podman / OCI labels).
//
// This served schema therefore carries no #*Input def. It exists ONLY to satisfy
// the host's "every plugin MUST ship a non-empty, base-splicing CUE schema" load
// gate (registerPluginUnitSchema). SELF-CONTAINED (package-less, references NO base
// def) so it compiles standalone (the SDK's serve-side check) AND splices onto the
// base (base ++ plugin is a def-name collision check, not a base-reference resolver).

// #McpPlugin documents the verb the plugin serves. mcp keeps its entire authoring
// contract (the #McpMethod enum + modifiers) on charly's core #Op, so there is no
// plugin_input to validate here.
#McpPlugin: {
	verb:     "mcp"
	contract: "mcp keeps its discriminator + modifiers on core #Op (no plugin_input)"
}
