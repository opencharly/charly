// schema/encinput.cue — the TRIVIAL Describe schema. The enc verb is invoked with
// the structured spec.EncExecInput over OpExecute (host→plugin, from the in-core enc
// shim), not an authored plugin_input, so it declares no #*Input. This def exists
// only to give the host plugin-schema gate a non-empty, base-spliceable schema.
#EncInput: {
	contract: "invoked via OpExecute with a spec.EncExecInput (host-prelifted per-volume gocryptfs plan + resolved passphrase); returns a spec.EncExecReply {error}"
}
