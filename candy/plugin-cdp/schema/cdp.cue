// This out-of-tree plugin's OWN CUE schema, served over the Describe channel.
//
// cdp is an EXTERNAL-CHARLY-VERB plugin: like the built-in wl/vnc/spice verbs, it KEEPS
// its `cdp:` discriminator + every modifier (tab/url/expression/selector/…) on charly's
// core closed #Op — authoring is UNCHANGED (`cdp: status`, not `plugin: cdp`). The
// method-name vocabulary (#CdpMethod) and the modifiers therefore live on core #Op, NOT
// here, so this plugin advertises a verb with NO plugin_input and NO input def. The host
// dispatches it through the registry exactly like a built-in (ResolveVerb("cdp") → the
// out-of-process grpcProvider → invokeVerbProvider hands it the FULL #Op as params_json,
// after pre-resolving the deployment's CDP port to a host-reachable DevTools base URL
// host-side — the plugin needs no podman / venue resolution).
//
// This served schema therefore carries no #*Input def. It exists ONLY to satisfy the
// host's "every plugin MUST ship a non-empty, base-splicing CUE schema" load gate
// (registerPluginUnitSchema). SELF-CONTAINED (package-less, references NO base def) so it
// compiles standalone (the SDK's serve-side check) AND splices onto the base (base ++
// plugin is a def-name collision check, not a base-reference resolver).

// #CdpPlugin documents the verb the plugin serves. cdp keeps its entire authoring
// contract (the #CdpMethod enum + modifiers) on charly's core #Op, so there is no
// plugin_input to validate here.
#CdpPlugin: {
	verb:     "cdp"
	contract: "cdp keeps its discriminator + modifiers on core #Op (no plugin_input)"
}
