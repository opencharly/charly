// schema/build.cue — the SELF-CONTAINED CUE schema candy/plugin-build ships over Describe
// (schema_cue). References NO base def so it compiles standalone (BuildCapabilities compiles it
// alone, failing loudly if broken) AND splices onto the base (the base ++ plugin splice detects a
// def-name collision — hence a UNIQUE name, never a #Build* already in the base).
//
// UNLIKE most plugins, this schema does NOT validate a per-word plugin_input: the build words
// (build:box / build:generate) carry a HOST-constructed spec.BuildRequest (built by BuildCmd /
// GenerateCmd from CLI flags), never a user-authored plugin_input, so both capabilities declare
// InputDef:"" and there is nothing to validate against a served schema. This def exists ONLY to
// satisfy the non-empty-schema load gate and to DOCUMENT the seam — it is never used for
// validation. The build request/reply wire shapes are the authoritative Go types
// spec.BuildRequest / spec.BuildReply (charly/spec/deploy_wire.go); the fields below mirror them
// for documentation.
#BuildDispatch: {
	// The host-constructed build request forwarded verbatim to HostBuild (informational).
	boxes?: [...string]
	tag?:              string
	dir?:              string
	include_disabled?: bool
	dev_local_pkg?:    bool
	push?:             bool
	platform?:         string
	cache?:            string
	no_cache?:         bool
	jobs?:             int
	podman_jobs?:      int
}
