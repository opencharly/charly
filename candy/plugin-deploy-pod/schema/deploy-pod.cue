// This out-of-tree plugin's OWN CUE schema, served over the Describe channel.
//
// deploy:pod is a SUBSTRATE: the deployment authors `pod: {image}` on charly's core #Deploy
// schema, NOT here — so this plugin advertises the substrate with NO plugin_input and NO
// input def (the host marshals the deployment's InstallPlan VIEWS + a venue descriptor into
// the OpExecute Invoke; for pod the overlay image is built HOST-SIDE by the pod lifecycle
// hook, so the plugin does not even walk the plans). This served schema therefore carries
// no #*Input def. It exists ONLY to satisfy the host's "every plugin MUST ship a non-empty,
// base-splicing CUE schema" load gate (registerPluginUnitSchema). SELF-CONTAINED
// (package-less, references NO base def) so it compiles standalone (the SDK's serve-side
// check) AND splices onto the base (base ++ plugin is a def-name collision check).

// #DeployPodPlugin documents the substrate the plugin serves. The substrate's authoring
// surface lives on core #Deploy; this is a marker def only.
#DeployPodPlugin: {
	substrate: "pod"
}
