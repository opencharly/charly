// This out-of-tree plugin's OWN CUE schema, served over the Describe channel.
//
// record is an EXTERNAL-CHARLY-VERB plugin: like the built-in cdp/vnc/mcp/spice verbs, it
// KEEPS its `record:` discriminator + every modifier
// (record_name/record_mode/record_fps/record_audio) on charly's core closed #Op —
// authoring is UNCHANGED (`record: start`, not `plugin: record`). The method-name
// vocabulary (#RecordMethod) and the modifiers therefore live on core #Op, NOT here, so
// this plugin advertises a verb with NO plugin_input and NO input def. The host dispatches
// it through the registry exactly like a built-in (ResolveVerb("record") → the
// out-of-process grpcProvider → invokeVerbProvider hands it the FULL #Op as params_json,
// plus the host's live DeployExecutor over the E3b reverse channel — record is EXEC-based,
// so the plugin drives the venue with RunCapture/GetFile rather than a pre-resolved
// endpoint).
//
// This served schema therefore carries no #*Input def. It exists ONLY to satisfy the
// host's "every plugin MUST ship a non-empty, base-splicing CUE schema" load gate
// (registerPluginUnitSchema). SELF-CONTAINED (package-less, references NO base def) so it
// compiles standalone (the SDK's serve-side check) AND splices onto the base (base ++
// plugin is a def-name collision check, not a base-reference resolver).

// #RecordPlugin documents the verb the plugin serves. record keeps its entire authoring
// contract (the #RecordMethod enum + modifiers) on charly's core #Op, so there is no
// plugin_input to validate here.
#RecordPlugin: {
	verb:     "record"
	contract: "record keeps its discriminator + modifiers (record_name/record_mode/record_fps/record_audio) on core #Op (no plugin_input)"
}
