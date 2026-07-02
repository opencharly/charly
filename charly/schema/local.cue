// CUE schema for the `local` kind (kind:local templates — target:local candy
// stacks). #Local validates ONE entry of the `local:` map. CLOSED (the Go
// loader decodes with KnownFields(true), so unknown keys are an error). Shared
// #Step from _common.cue.

#Local: {
	// Ordered candy stack applied to the host. Required (empty list permitted —
	// a staged name-reservation stub; the loader warns, not errors).
	candy!: [...#CandyRef]
	install_opts?: #InstallOpts @go(InstallOpts,optional=nillable)
	env?: [...#EnvVar]
	description?: string & !=""
	plan?: [...#Step]
}

// #CandyRef / #EnvVar / #InstallOpts now live in _common.cue (shared with
// deploy + pod + candy).
