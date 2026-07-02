// schema/examplestructkind.cue — the SELF-CONTAINED CUE def validating the example STRUCTURAL
// external KIND's authored SCALAR body. Ships over Describe (schema_cue); references no base def so
// it compiles standalone (BuildCapabilities compiles it alone, failing loudly if broken).
//
// This def is CLOSED (the default) and validates ONLY op.Params — the kind-specific scalar body.
// The authored resource-MEMBER children are NOT part of this def: they cannot ride op.Params (a
// closed def rejects them), so the host pre-decodes them and threads them via op.Env
// (spec.StructuralKindLoadEnv, F5 authored-member input-threading). This mirrors the shape the
// `group` externalization reuses — deploy-config scalars in the value, members via the env channel.
#ExamplestructkindInput: {
	// marker is an OPTIONAL kind-specific scalar (op.Params) — proving kind-specific config decode
	// coexists with host-threaded members (op.Env). Stamped into the reply Description.
	marker?: string
	// disposable / lifecycle / description are the deploy-config passthrough the plugin maps into
	// its spec.Deploy reply, so a structural node can BE a disposable check bed — the exact shape
	// the group cutover needs. (The members ride op.Env, never this closed value.)
	disposable?:  bool
	lifecycle?:   string
	description?: string
}
