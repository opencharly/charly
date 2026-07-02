// This out-of-tree plugin's OWN CUE schema, served over the Describe channel.
//
// adb is an EXTERNAL-CHARLY-VERB plugin: like the built-in cdp/vnc/wl verbs, it
// KEEPS its `adb:` discriminator + every modifier (arg/apk/property/artifact/key/
// app_id/source/arch/…) on charly's core closed #Op — authoring is UNCHANGED
// (`adb: devices`, not `plugin: adb`). The method-name vocabulary (#AdbMethod) and
// the modifiers therefore live on core #Op, NOT here, so this plugin advertises a
// verb with NO plugin_input and NO input def. The host dispatches it through the
// registry exactly like a built-in (ResolveVerb("adb") → the out-of-process
// grpcProvider → invokeVerbProvider hands it the FULL #Op as params_json).
//
// This served schema therefore carries no #*Input def. It exists ONLY to satisfy
// the host's "every plugin MUST ship a non-empty, base-splicing CUE schema" load
// gate (registerPluginUnitSchema). SELF-CONTAINED (package-less, references NO base
// def) so it compiles standalone (the SDK's serve-side check) AND splices onto the
// base (base ++ plugin is a def-name collision check, not a base-reference resolver).

// #AdbPlugin documents the verb the plugin serves. adb keeps its entire authoring
// contract (the #AdbMethod enum + modifiers) on charly's core #Op, so there is no
// plugin_input to validate here.
#AdbPlugin: {
	verb:     "adb"
	contract: "adb keeps its discriminator + modifiers on core #Op (no plugin_input)"
}
