// The BUILT-IN `sidecar` plugin's OWN CUE schema — the typed input for the `sidecar`
// KIND (the reusable sidecar-container template library, formerly a core `sidecar:`
// kind decoded into the typed core map uf.Sidecar). It is the SINGLE SOURCE for this
// plugin's params, used two ways (the same contract the reference exampleprobe/process,
// the package-group/agent/module plugins, and core `spec` use):
//
//  1. GENERATE the Go param struct — `cue exp gengotypes` (driven by task cue:gen,
//     which wraps this with `package params` + `@go(params)`) emits
//     ../params/cue_types_gen.go.
//  2. VALIDATE authored input AT RUNTIME — the builtin serves this source over the
//     Describe channel (InProcTransport) exactly like an external serves it over
//     gRPC; the host splices it onto the base (base ++ plugin) and validates every
//     authored `sidecar:` entity body against #SidecarInput BEFORE runPluginKind
//     dispatches (validateAuthoredPluginInput(ClassKind, "sidecar", …)) — the
//     kind-class analogue of the verb plugin_input gate.
//
// SELF-CONTAINED: it references NO base def — every shared shape (#ScStrMap value,
// #ScSidecarSecret, #ScSidecarVolume, #ScSecurity, #ScSize) is reproduced standalone
// here, so it compiles standalone (gengotypes + the load-gate compile) AND splices
// onto the base (the base ++ plugin splice exists to detect a def-name collision with
// the base, not to resolve base refs). It is a faithful reproduction of the core
// #Sidecar (schema/sidecar.cue) — the same authored WIRE keys, so the host validates a
// real sidecar entity (incl. the binary-embedded `tailscale` template), and the
// plugin's Invoke canonicalises the body back through the core spec.Sidecar type (which
// SidecarDef aliases and the deploy/quadlet code consumes via the Sidecars() accessor).
//
// NAME: in node-form the entity name is the top-level node KEY, not a body field, so
// the assembled entity body NEVER carries `name`; #Sidecar has no name field either, so
// there is nothing to make optional here.
#SidecarInput: {
	description?: string & !=""
	image?:       string & !=""
	// env / parameter are string-coercible maps (#StrMap: values may be a quoted
	// string / number / bool). parameter "" is the "deploy must supply" sentinel.
	env?: {[string]: (string | number | bool)}
	parameter?: {[string]: (string | number | bool)}
	secret?: [...#ScSidecarSecret]
	volume?: [...#ScSidecarVolume]
	security?: #ScSecurity
}

// reproduces #SidecarSecret standalone.
#ScSidecarSecret: {
	name:         string & !=""
	env:          string & !=""
	env_from?:    string
	description?: string & !=""
}

// reproduces #SidecarVolume standalone.
#ScSidecarVolume: {
	name: string & !=""
	path: string & =~"^/"
}

// reproduces #Security (schema/_common.cue) standalone.
#ScSecurity: {
	privileged?: bool
	cgroupns?:   "host" | "private" | ""
	cap_add?: [...string]
	devices?: [...string]
	security_opt?: [...string]
	ipc_mode?: "host" | "private" | "shareable" | ""
	shm_size?: #ScSize
	group_add?: [...string]
	mount?: [...string]
	memory_max?:      #ScSize
	memory_high?:     #ScSize
	memory_swap_max?: #ScSize
	cpus?:            string & =~"^[0-9]+(\\.[0-9]+)?$"
}

// reproduces #Size (schema/_common.cue) standalone.
#ScSize: string & =~"^[0-9]+(\\.[0-9]+)?[kKmMgG]?$"
