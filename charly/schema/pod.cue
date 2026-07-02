// CUE schema for the `pod` kind. #Pod validates ONE entry of the `pod:` map
// (PodSpec). Most projects declare no kind:pod entries (the real corpus is all
// `pod: {}`); the schema is modeled directly from the struct. CLOSED — every
// PodSpec field is modeled, so an unknown key is a typo. Shared defs REFERENCED,
// not redefined (R3): #Step/#EnvVar/#CandyRef from _common.cue,
// #DeploySecret from deploy.cue, #Sidecar from sidecar.cue.

#Pod: {
	// References a kind:box (bare lowercase-hyphenated name or remote ref).
	// Optional: the Go field has no non-empty validator.
	box?: #CandyRef
	// Each entry is a SidecarConfig wrapper: a single-key map of named
	// sidecar templates (PodSpec.Sidecar []SidecarConfig).
	sidecar?: [...#PodSidecar]
	secret?: [...#DeploySecret]
	env_default?: [...#EnvVar] @go(EnvDefaults)
	plan?: [...#Step]
}

// #PodSidecar mirrors Go SidecarConfig: a single-key wrapper around a map of
// named sidecar templates. CLOSED (the struct has exactly one field).
#PodSidecar: {
	sidecar: {[string]: #Sidecar}
}
