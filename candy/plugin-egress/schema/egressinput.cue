// schema/egressinput.cue — the TRIVIAL Describe schema. The real egress validation
// schemas are held INTERNALLY (egress-schemas/, compiled in newProvider), NOT served over
// Describe — so the vendored package+import cloud_config never has to join the single-blob
// Describe concat. This def exists only to give the host plugin-schema gate a non-empty,
// base-spliceable schema. The egress verb is invoked with OpValidate({kind,label,mode,data}),
// not a structured plugin_input, so it declares no #*Input.
#EgressInput: {
	contract: "invoked via OpValidate with {kind,label,mode,data}; validates an egress artifact against the kind's internal CUE schema"
}
