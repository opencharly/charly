// schema/examplebootstrap.cue — the SELF-CONTAINED CUE schema served over Describe. A bootstrap
// plugin is invoked with the RAW config (OpBootstrap), not a structured plugin_input, so it ships
// no #*Input def; this doc def exists only to satisfy the host's non-empty/base-splicing schema
// load gate (registerPluginUnitSchema) + the params codegen loop.
#ExamplebootstrapPlugin: {
	phase:    "bootstrap"
	contract: "invoked with the raw project config bytes before validation; this no-op returns them unchanged"
}
