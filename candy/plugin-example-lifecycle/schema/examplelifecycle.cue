// schema/examplelifecycle.cue — the SELF-CONTAINED CUE def validating the example lifecycle deploy
// substrate's deploy input. References no base def so it compiles standalone (BuildCapabilities
// compiles it alone, failing loudly if broken).
#ExamplelifecycleInput: {
	// marker is an optional opaque field — the example substrate carries no real config; it exists
	// so a deploy authoring `examplelifecycle: { ... }` validates against a real def.
	marker?: string
}
