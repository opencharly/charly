// This out-of-tree plugin's OWN CUE schema, served over the Describe channel.
//
// appium is an EXTERNAL-CHARLY-VERB plugin: like the built-in cdp/vnc/wl verbs, it
// KEEPS its `appium:` discriminator + every modifier (selector/caps/strategy/app_id/
// activity/…) on charly's core closed #Op — authoring is UNCHANGED (`appium: status`,
// not `plugin: appium`). The method-name vocabulary (#AppiumMethod) and the modifiers
// therefore live on core #Op, NOT here, so this plugin advertises a verb with NO
// plugin_input and NO input def. The host dispatches it through the registry exactly
// like a built-in (ResolveVerb("appium") → the out-of-process grpcProvider →
// invokeVerbProvider hands it the FULL #Op as params_json).
//
// This served schema therefore carries no #*Input def. It exists ONLY to satisfy the
// host's "every plugin MUST ship a non-empty, base-splicing CUE schema" load gate
// (registerPluginUnitSchema). SELF-CONTAINED (package-less, references NO base def) so
// it compiles standalone (the SDK's serve-side check) AND splices onto the base
// (base ++ plugin is a def-name collision check, not a base-reference resolver).

// #AppiumPlugin documents the verb the plugin serves. appium keeps its entire
// authoring contract (the #AppiumMethod enum + modifiers) on charly's core #Op, so
// there is no plugin_input to validate here.
#AppiumPlugin: {
	verb:     "appium"
	contract: "appium keeps its discriminator + modifiers on core #Op (no plugin_input)"
}
