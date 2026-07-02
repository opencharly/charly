// This out-of-tree plugin's OWN CUE schema, served over the Describe channel.
//
// wl is an EXTERNAL-CHARLY-VERB plugin: like the built-in cdp/vnc/mcp/record/dbus verbs, it
// KEEPS its `wl:` discriminator + every modifier
// (x/y/x2/y2/direction/amount/target/text/key/combo/command/action/query/artifact) on
// charly's core closed #Op — authoring is UNCHANGED (`wl: screenshot`, not `plugin: wl`). The
// method-name vocabulary (#WlMethod) and the modifiers therefore live on core #Op, NOT here,
// so this plugin advertises a verb with NO plugin_input and NO input def. The host dispatches
// it through the registry exactly like a built-in (ResolveVerb("wl") → the out-of-process
// grpcProvider → invokeVerbProvider hands it the FULL #Op as params_json, plus the host's
// live DeployExecutor over the E3b reverse channel — wl is EXEC-based, so the plugin drives
// the venue's compositor with RunCapture/GetFile rather than a pre-resolved endpoint).
//
// This served schema therefore carries no #*Input def. It exists ONLY to satisfy the host's
// "every plugin MUST ship a non-empty, base-splicing CUE schema" load gate
// (registerPluginUnitSchema). SELF-CONTAINED (package-less, references NO base def) so it
// compiles standalone (the SDK's serve-side check) AND splices onto the base (base ++ plugin
// is a def-name collision check, not a base-reference resolver).

// #WlPlugin documents the verb the plugin serves. wl keeps its entire authoring contract (the
// #WlMethod enum + modifiers) on charly's core #Op, so there is no plugin_input to validate here.
#WlPlugin: {
	verb:     "wl"
	contract: "wl keeps its discriminator + modifiers (x/y/x2/y2/direction/amount/target/text/key/combo/command/action/query/artifact) on core #Op (no plugin_input)"
}
