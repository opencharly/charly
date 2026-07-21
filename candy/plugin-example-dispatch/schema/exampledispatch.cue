// schema/exampledispatch.cue — the SELF-CONTAINED CUE def for the F10 dispatch demo verb's input.
#ExampledispatchInput: {
	target_word?:     string // a verb word the host resolves + invokes on this plugin's behalf (plugin↔plugin)
	build_candy_dir?: string // a candy dir the host builds a plugin binary for (host-build)
	build_name?:      string
	// peer_cmd, when set, is forwarded to exampledispatchpeer, which RunCaptures it over
	// whatever executor the host threads onto it (S1 proof — the venue-scoped-executor-session
	// seam: the executor is this plugin's own by default, or a fresh one the host materialized
	// from venue_descriptor below).
	peer_cmd?: string
	// venue_descriptor, when set, rides InvokeProviderOpts on the InvokeProvider call to
	// target_word — the host re-materializes a FRESH executor from it (mirrors
	// spec.VenueDescriptor) instead of forwarding this plugin's own executor.
	venue_descriptor?: {
		kind:             "shell" | "ssh"
		user?:            string
		host?:            string
		port?:            int
		args?: [...string]
		connect_timeout?: int
	}
}
