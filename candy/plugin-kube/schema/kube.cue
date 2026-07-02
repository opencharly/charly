// This out-of-tree plugin's OWN CUE schema, served over the Describe channel.
//
// kube is an EXTERNAL-CHARLY-VERB plugin: like the built-in cdp/vnc/wl verbs, it
// KEEPS its `kube:` discriminator + every modifier (name/namespace/label/cluster/
// manifest/kube_kind/kube_context/kubeconfig/kube_count/kube_resource/kube_group/
// kube_version/…) on charly's core closed #Op — authoring is UNCHANGED
// (`kube: nodes`, not `plugin: kube`). The method-name vocabulary (#KubeMethod) and
// the modifiers therefore live on core #Op, NOT here, so this plugin advertises a
// verb with NO plugin_input and NO input def. The host dispatches it through the
// registry exactly like a built-in (ResolveVerb("kube") → the out-of-process
// grpcProvider → invokeVerbProvider hands it the FULL #Op as params_json, after
// pre-resolving any --cluster profile to a concrete kubeconfig context host-side).
//
// This served schema therefore carries no #*Input def. It exists ONLY to satisfy
// the host's "every plugin MUST ship a non-empty, base-splicing CUE schema" load
// gate (registerPluginUnitSchema). SELF-CONTAINED (package-less, references NO base
// def) so it compiles standalone (the SDK's serve-side check) AND splices onto the
// base (base ++ plugin is a def-name collision check, not a base-reference resolver).

// #KubePlugin documents the verb the plugin serves. kube keeps its entire authoring
// contract (the #KubeMethod enum + modifiers) on charly's core #Op, so there is no
// plugin_input to validate here.
#KubePlugin: {
	verb:     "kube"
	contract: "kube keeps its discriminator + modifiers on core #Op (no plugin_input)"
}
