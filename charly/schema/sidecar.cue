// CUE schema for the `sidecar` kind. #Sidecar validates ONE sidecar-template
// entity (a value of the `sidecar:` map, e.g. the embedded default library, or
// a per-deploy override). Mirrors Go SidecarDef. CLOSED (an unknown key is a
// typo). Every field optional (struct tags are omitempty; an override supplies a
// subset).

#Sidecar: {
	description?: string & !=""
	image?:       string & !=""
	// env / parameter are map[string]string — values MUST be strings (quote
	// YAML bools/numbers). parameter "" is the "deploy must supply" sentinel.
	env?:       #StrMap
	parameter?: #StrMap
	secret?: [...#SidecarSecret]
	volume?: [...#SidecarVolume]
	security?: #Security @go(Security,optional=nillable)
}

#SidecarSecret: {
	name:         string & !=""
	env:          string & !=""
	env_from?:    string @go(EnvFrom) // Go text/template, rendered later; CUE checks string only
	description?: string & !=""
}

#SidecarVolume: {
	name: string & !=""
	path: string & =~"^/"
}

// #Security + #Size now live in _common.cue (shared by box/candy/deploy/sidecar).
