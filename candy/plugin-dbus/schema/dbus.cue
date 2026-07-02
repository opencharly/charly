// This out-of-tree plugin's OWN CUE schema, served over the Describe channel.
//
// dbus is an EXTERNAL-CHARLY-VERB plugin: like the built-in cdp/vnc/mcp/record verbs, it
// KEEPS its `dbus:` discriminator + every modifier (dest/path/method/args/text/description)
// on charly's core closed #Op — authoring is UNCHANGED (`dbus: list`, not `plugin: dbus`).
// The method-name vocabulary (#DbusMethod) and the modifiers therefore live on core #Op, NOT
// here, so this plugin advertises a verb with NO plugin_input and NO input def. The host
// dispatches it through the registry exactly like a built-in (ResolveVerb("dbus") → the
// out-of-process grpcProvider → invokeVerbProvider hands it the FULL #Op as params_json, plus
// the host's live DeployExecutor over the E3b reverse channel — dbus is EXEC-based, so the
// plugin drives the venue's session bus with gdbus over RunCapture rather than a pre-resolved
// endpoint).
//
// This served schema therefore carries no #*Input def. It exists ONLY to satisfy the host's
// "every plugin MUST ship a non-empty, base-splicing CUE schema" load gate
// (registerPluginUnitSchema). SELF-CONTAINED (package-less, references NO base def) so it
// compiles standalone (the SDK's serve-side check) AND splices onto the base (base ++ plugin
// is a def-name collision check, not a base-reference resolver).

// #DbusPlugin documents the verb the plugin serves. dbus keeps its entire authoring contract
// (the #DbusMethod enum + modifiers) on charly's core #Op, so there is no plugin_input to
// validate here.
#DbusPlugin: {
	verb:     "dbus"
	contract: "dbus keeps its discriminator + modifiers (dest/path/method/args/text/description) on core #Op (no plugin_input)"
}
