// schema/gpuinput.cue — the TRIVIAL Describe schema. The gpu verb is invoked with the
// structured spec.GpuProbeInput over OpRun (by charly's in-core Detect* shims), not an
// authored plugin_input, so it declares no #*Input. This def exists only to give the
// host plugin-schema gate a non-empty, base-spliceable schema.
#GpuInput: {
	contract: "invoked via OpRun with a spec.GpuProbeInput (action-multiplexed host probe); returns a spec.GpuProbeReply"
}
