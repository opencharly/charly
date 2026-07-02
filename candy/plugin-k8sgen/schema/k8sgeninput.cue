// schema/k8sgeninput.cue — the TRIVIAL Describe schema. The k8sgen verb is invoked
// with the structured spec.K8sGenInput over OpEmit, not an authored plugin_input,
// so it declares no #*Input. This def exists only to give the host plugin-schema
// gate a non-empty, base-spliceable schema.
#K8sgenInput: {
	contract: "invoked via OpEmit with a spec.K8sGenInput; returns a spec.K8sGenReply of relative-pathed Kustomize manifest docs"
}
