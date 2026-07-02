// This out-of-tree plugin's OWN CUE schema, served over the Describe channel.
//
// spice is an EXTERNAL-CHARLY-VERB plugin: like the built-in cdp/vnc/wl verbs, it
// KEEPS its `spice:` discriminator + every modifier (x/y/text/key/artifact/…) on
// charly's core closed #Op — authoring is UNCHANGED (`spice: status`, not
// `plugin: spice`). The method-name vocabulary (#SpiceMethod) and the modifiers
// therefore live on core #Op, NOT here, so this plugin advertises a verb with NO
// plugin_input and NO input def. The host dispatches it through the registry
// exactly like a built-in (ResolveVerb("spice") → the out-of-process grpcProvider →
// invokeVerbProvider hands it the FULL #Op as params_json, after pre-resolving the
// VM's live SPICE endpoint to a dialable address host-side — the plugin needs no
// go-libvirt).
//
// This served schema therefore carries no #*Input def. It exists ONLY to satisfy
// the host's "every plugin MUST ship a non-empty, base-splicing CUE schema" load
// gate (registerPluginUnitSchema). SELF-CONTAINED (package-less, references NO base
// def) so it compiles standalone (the SDK's serve-side check) AND splices onto the
// base (base ++ plugin is a def-name collision check, not a base-reference resolver).

// #SpicePlugin documents the verb the plugin serves. spice keeps its entire
// authoring contract (the #SpiceMethod enum + modifiers) on charly's core #Op, so
// there is no plugin_input to validate here.
#SpicePlugin: {
	verb:     "spice"
	contract: "spice keeps its discriminator + modifiers on core #Op (no plugin_input)"
}
