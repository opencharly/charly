// The BUILT-IN `agent` plugin's OWN CUE schema — the typed input for the `agent`
// KIND (the AI-CLI grader catalog, formerly a core `agent:` kind decoded into the
// typed core map uf.Agent). It is the SINGLE SOURCE for this plugin's params, used
// two ways (the same contract the reference exampleprobe/process, the package-group
// plugin, and core `spec` use):
//
//  1. GENERATE the Go param struct — `cue exp gengotypes` (driven by task cue:gen,
//     which wraps this with `package params` + `@go(params)`) emits
//     ../params/cue_types_gen.go.
//  2. VALIDATE authored input AT RUNTIME — the builtin serves this source over the
//     Describe channel (InProcTransport) exactly like an external serves it over
//     gRPC; the host splices it onto the base (base ++ plugin) and validates every
//     authored `agent:` entity body against #AgentInput BEFORE runPluginKind
//     dispatches (validateAuthoredPluginInput(ClassKind, "agent", …)) — the
//     kind-class analogue of the verb plugin_input gate.
//
// SELF-CONTAINED: it references NO base def — every shared shape (#AgDuration,
// #AgCredentialMount) is reproduced standalone here, so it compiles standalone
// (gengotypes + the load-gate compile) AND splices onto the base (the base ++ plugin
// splice exists to detect a def-name collision with the base, not to resolve base
// refs). It is a faithful reproduction of the core #Agent (schema/agent.cue) — the
// same authored WIRE keys, so the host validates a real agent entity, and the
// plugin's Invoke canonicalises the body back through the core spec.Agent type (which
// AgentConfig still aliases and the whole iterate/check harness consumes).
//
// NAME: in node-form the entity name is the top-level node KEY, not a body field, so
// the assembled entity body NEVER carries `name`; #Agent has no name field either, so
// there is nothing to make optional here.
#AgentInput: {
	description?: string & !=""
	command: [string, ...string] // >=1, all strings
	prompt_via:                       *"argv" | "file"
	version_command?: [...string]
	timeout?: #AgDuration
	env?: {[string]: string}
	working_dir?: string & !=""
	credential?: [...#AgCredentialMount]
	progress_check_interval?:         #AgDuration
	progress_no_improvement_timeout?: #AgDuration
	output_format:                    *"" | "stream-json"
}

// reproduces #Duration (schema/_common.cue) standalone.
#AgDuration: string & =~"^[0-9]+(\\.[0-9]+)?(ns|us|µs|ms|s|m|h)([0-9]+(\\.[0-9]+)?(ns|us|µs|ms|s|m|h))*$"

// reproduces #CredentialMount (schema/agent.cue) standalone.
#AgCredentialMount: {
	src:       string & !=""
	dst:       string & !=""
	mode?:     "copy" | "bind"
	optional?: bool
}

// The declarative team kind is plugin-owned as well. These standalone names
// reproduce the shared SDK wire contract without depending on, or colliding
// with, the host's base schema during plugin Describe splicing.
#AgentTeamInput: {
	description?: string & !=""
	agents: [#AtMember, ...#AtMember]
	edges?: [...#AtEdge]
	coordinator?:     string
	concurrency?:     int & >=1
	evidence_policy?: "target" | "coordinator" | "both" @go(EvidencePolicy)
}

#AtMember: {
	name:    string & !=""
	runtime: string & !=""
	role?:   string
	target?: #AtTarget
	terminal_profile?: #AtTerminalProfile
}

#AtEdge: {
	from: string & !=""
	to:   string & !=""
	allow?: [...string]
}

#AtTarget: {
	hops?: [...#AtTargetHop]
	deployment?:  string
	instance?:    string
	working_dir?: string
}

#AtTargetHop: {
	transport:      "inproc" | "exec" | "ssh" | "grpc" | "tmux"
	address?:       string
	user?:          string
	port?:          int & >0 & <=65535
	identity_file?: string
	command?: [...string]
	env?: {[string]: string}
	options?: {[string]: string}
}

#AtTerminalProfile: {
	name:       string & !=""
	entrypoint: [string, ...string]
	working_dir?: string
	env?: {[string]: string}
	cols: *120 | (int & >0 & <=1000)
	rows: *40 | (int & >0 & <=1000)
	readiness?: {...}
	semantic_adapter?: string
	keys?: [...string]
	signals?: [...string]
	persistence?: "none" | "detach" | "required"
	transcript?:  "none" | "raw" | "screen" | "both"
}
