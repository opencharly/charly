// schema/examplekind.cue — the SELF-CONTAINED CUE def validating the example external KIND's
// authored body (`examplekind: { marker: ... }`). Ships over Describe (schema_cue); references no
// base def so it compiles standalone (BuildCapabilities compiles it alone, failing loudly).
#ExamplekindInput: {
	// marker is the entity body's only field — proving the authored `kind: examplekind` body
	// round-trips through the host's #ExamplekindInput validation to the plugin's OpLoad and into
	// uf.PluginKinds["examplekind"][<name>].
	marker?: string
}
