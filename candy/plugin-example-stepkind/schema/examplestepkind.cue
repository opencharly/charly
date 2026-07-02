// schema/examplestepkind.cue — the SELF-CONTAINED CUE def validating the example external
// step kind's opaque plugin_input (the Payload carried as "external:examplestepkind"). Ships
// over Describe (schema_cue) + drives the generated Go params; references no base def so it
// compiles standalone (BuildCapabilities compiles it alone, failing loudly if broken).
#ExamplestepkindInput: {
	// marker is the string the step writes to its marker file — at DEPLOY (OpExecute) the venue
	// marker /tmp/charly-examplestepkind/marker, at BUILD (OpEmit, F-STEP-EMIT) the baked image
	// marker /etc/examplestepkind-build-baked — proving the OPAQUE payload round-trips through the
	// IR (InstallStepView.Payload) to BOTH the plugin's OpExecute and its OpEmit.
	marker?: string
}
