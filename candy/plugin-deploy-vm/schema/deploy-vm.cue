// This out-of-tree plugin's OWN CUE schema, served over the Describe channel.
//
// deploy:vm is a SUBSTRATE: the deployment authors `vm: {from}` on charly's core #Vm /
// #Deploy schema, NOT here — so this plugin advertises the substrate with NO plugin_input
// and NO input def (the host marshals the deployment's InstallPlan VIEWS + a venue
// descriptor into the OpExecute Invoke; the plugin walks them via kit.WalkPlans over the
// GUEST SSHExecutor the host's vm lifecycle hook built). This served schema therefore
// carries no #*Input def. It exists ONLY to satisfy the host's "every plugin MUST ship a
// non-empty, base-splicing CUE schema" load gate (registerPluginUnitSchema). SELF-CONTAINED
// (package-less, references NO base def) so it compiles standalone (the SDK's serve-side
// check) AND splices onto the base (base ++ plugin is a def-name collision check).

// #DeployVmPlugin documents the substrate the plugin serves. The substrate's authoring
// surface lives on core #Vm / #Deploy; this is a marker def only.
#DeployVmPlugin: {
	substrate: "vm"
}
