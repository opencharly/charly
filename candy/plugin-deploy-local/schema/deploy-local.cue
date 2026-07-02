// This out-of-tree plugin's OWN CUE schema, served over the Describe channel.
//
// deploy:local is a SUBSTRATE: the deployment authors `local: {from, host, user,
// ssh_args}` on charly's core #Local / #Deploy schema, NOT here — so this plugin
// advertises the substrate with NO plugin_input and NO input def (the host marshals the
// deployment's InstallPlan VIEWS + a venue descriptor into the OpExecute Invoke; the
// plugin walks them via kit.WalkPlans). This served schema therefore carries no #*Input
// def. It exists ONLY to satisfy the host's "every plugin MUST ship a non-empty,
// base-splicing CUE schema" load gate (registerPluginUnitSchema). SELF-CONTAINED
// (package-less, references NO base def) so it compiles standalone (the SDK's serve-side
// check) AND splices onto the base (base ++ plugin is a def-name collision check).

// #DeployLocalPlugin documents the substrate the plugin serves. The substrate's authoring
// surface lives on core #Local; this is a marker def only.
#DeployLocalPlugin: {
	substrate: "local"
}
