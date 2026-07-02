// This out-of-tree plugin's OWN CUE schema, served over the Describe channel.
//
// vnc is an EXTERNAL-CHARLY-VERB plugin: like the built-in cdp/mcp/spice verbs, it KEEPS
// its `vnc:` discriminator + every modifier (x/y/text/key/artifact/method/params) on
// charly's core closed #Op — authoring is UNCHANGED (`vnc: status`, not `plugin: vnc`). The
// method-name vocabulary (#VncMethod) and the modifiers therefore live on core #Op, NOT
// here, so this plugin advertises a verb with NO plugin_input and NO input def. The host
// dispatches it through the registry exactly like a built-in (ResolveVerb("vnc") → the
// out-of-process grpcProvider → invokeVerbProvider hands it the FULL #Op as params_json,
// after pre-resolving the deployment's VNC endpoint to a host-reachable RFB address
// host-side — the plugin needs no podman / venue / libvirt resolution).
//
// This served schema therefore carries no #*Input def. It exists ONLY to satisfy the
// host's "every plugin MUST ship a non-empty, base-splicing CUE schema" load gate
// (registerPluginUnitSchema). SELF-CONTAINED (package-less, references NO base def) so it
// compiles standalone (the SDK's serve-side check) AND splices onto the base (base ++
// plugin is a def-name collision check, not a base-reference resolver).

// #VncPlugin documents the verb the plugin serves. vnc keeps its entire authoring
// contract (the #VncMethod enum + modifiers) on charly's core #Op, so there is no
// plugin_input to validate here.
#VncPlugin: {
	verb:     "vnc"
	contract: "vnc keeps its discriminator + modifiers on core #Op (no plugin_input)"
}
