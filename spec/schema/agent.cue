// CUE schema for the `agent` kind. #Agent validates ONE value of the `agent:`
// map (AgentConfig — the AI-CLI grader catalog). CLOSED (AgentConfig is fully
// enumerated; an unknown key is a typo). No #Step (an agent has no plan).

#Agent: {
	description?: string & !=""
	command: [string, ...string] // >=1, all strings
	prompt_via:                  *"argv" | "file" @go(PromptVia)
	version_command?: [...string] @go(VersionCommand)
	timeout?:     #Duration
	env?:         #StrMap
	working_dir?: string & !="" @go(WorkingDir)
	credential?: [...#CredentialMount]
	progress_check_interval?:         #Duration           @go(ProgressCheckInterval)
	progress_no_improvement_timeout?: #Duration           @go(ProgressNoImprovementTimeout)
	output_format:                    *"" | "stream-json" @go(OutputFormat)
}

#CredentialMount: {
	src:       string & !=""
	dst:       string & !=""
	mode?:     "copy" | "bind"
	optional?: bool
}

// #Duration now lives in _common.cue (shared by agent + deploy + #Op).

// --- resolve-to-envelope wire types (Cutover E; SDD conversion, per the
// standing operator directive: a hand-written wire struct not yet CUE-sourced
// is conversion-in-progress, never a sanctioned exception). candy/plugin-agent
// resolves an authored Agent (an AI-CLI grader-catalog entry) into an
// AgentExecSpec: a GENERIC "launch + version-capture + iterate-poll a
// long-running CLI" descriptor the kernel's check/iterate harness consumes
// WITHOUT importing the concrete `agent` kind. The kernel stores agent bodies
// opaquely and the agent plugin owns the authored schema + default
// application. Plain structs — gengotypes generates them faithfully, no
// disjunction needed.

// #AgentResolveInput is candy/plugin-agent's OpResolve input: the opaque agent
// bodies (the name-keyed AI-CLI catalog) + the selected agent name ("" picks
// the sole entry, or errors when several are configured).
#AgentResolveInput: {
	agents?: {[string]: bytes} @go(Agents,type=map[string]RawBody)
	name?:   string @go(Name)
}

// #AgentResolveReply is the OpResolve reply: the resolved exec spec + the
// chosen agent's catalog name.
#AgentResolveReply: {
	spec?: #AgentExecSpec @go(Spec,optional=nillable)
	name?: string @go(Name)
}

// #AgentExecSpec is a fully-resolved long-running-CLI invocation descriptor —
// the resolve-to-envelope form of an agent, with Go-level defaults applied by
// the plugin (Timeout, PromptVia). Kind-agnostic: it describes HOW to launch a
// CLI, capture its version, and poll it in a plateau-bounded loop — not
// "agent-kind" knowledge.
#AgentExecSpec: {
	command?: [...string] @go(Command)
	prompt_via?: string @go(PromptVia)
	version_command?: [...string] @go(VersionCommand)
	timeout?:     #Duration @go(Timeout)
	env?:         #StrMap   @go(Env)
	working_dir?: string    @go(WorkingDir)
	credential?: [...#CredentialMount] @go(Credential)
	progress_check_interval?:         #Duration @go(ProgressCheckInterval)
	progress_no_improvement_timeout?: #Duration @go(ProgressNoImprovementTimeout)
	output_format?:                   string    @go(OutputFormat)
}
