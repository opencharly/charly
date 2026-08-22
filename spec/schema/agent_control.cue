// Generic agent-control, target, and terminal contracts. These definitions own
// the JSON payloads carried inside protocol.ChannelFrame; protobuf owns only the
// transport envelope. Runtime names and transport combinations are data.

#UUIDv7: string & =~"^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$"

#TargetHop: {
	transport:      "inproc" | "exec" | "ssh" | "grpc" | "tmux"
	address?:       string
	user?:          string
	port?:          int & >0 & <=65535
	identity_file?: string @go(IdentityFile)
	command?: [...string]
	env?: #StrMap
	// Transport-native argv options expressed as deterministic key/value
	// data. SSH renders these as repeated `-o key=value` arguments; no shell
	// interpolation is involved.
	options?: #StrMap
}

#TargetSpec: {
	// Ordered outer-to-inner route after placement. An empty route is the
	// selected process. deployment/instance first place that process in any
	// Charly deployment; the remaining route composes identically there.
	hops?: [...#TargetHop]
	deployment?:  string
	instance?:    string
	working_dir?: string @go(WorkingDir)
}

#ProcessLaunch: {
	argv: [string & !="", ...string]
	working_dir?: string @go(WorkingDir)
	env?: #StrMap
}

#AgentRuntimeCapability: {
	provider:         string & !=""
	protocol_version: int & >=1 @go(ProtocolVersion,type=int)
	entrypoint?: [...string]
	profiles?: [...string]
	interaction_modes?: [...("structured" | "terminal")] @go(InteractionModes)
	persistence?:         "none" | "session" | "required"
	semantic_completion?: bool @go(SemanticCompletion)
}

#AgentSession: {
	id:           #UUIDv7 @go(ID)
	runtime:      string & !=""
	target:       #TargetSpec
	state:        "new" | "active" | "detached" | "closed" | "failed"
	created_at:   string @go(CreatedAt)
	updated_at:   string @go(UpdatedAt)
	storage_ref?: string @go(StorageRef)
	metadata?:    #StrMap
	terminal_profile?: #TerminalProfile @go(TerminalProfile,optional=nillable)
}

#AgentRunRequest: {
	id:              #UUIDv7       @go(ID)
	session_id:      #UUIDv7       @go(SessionID)
	request_id:      #UUIDv7       @go(RequestID)
	idempotency_key: string & !="" @go(IdempotencyKey)
	prompt?:         string
	params?: {...} @go(Params,type=map[string]any)
	resume?: bool
}

// Provider-reported durable session correlation. Runtime-specific session
// state remains on the target; this is the only binding replicated to the
// controller.
#AgentSessionBinding: {
	storage_ref: string & !="" @go(StorageRef)
}

#AgentAbortControl: {
	run_id:       #UUIDv7 @go(RunID)
	request_id:   #UUIDv7 @go(RequestID)
	requested_at: string  @go(RequestedAt)
}

#AgentEvent: {
	run_id:   #UUIDv7 @go(RunID)
	sequence: int & >=1
	type:     "started" | "message" | "tool" | "delegation" | "settled" | "status" | "completed" | "aborted" | "failed"
	time:     string
	payload?: {...} @go(Payload,type=map[string]any)
}

#AgentTeamMember: {
	name:    string & !=""
	runtime: string & !=""
	role?:   string
	target?: #TargetSpec
	terminal_profile?: #TerminalProfile @go(TerminalProfile,optional=nillable)
}

#AgentDelegationEdge: {
	from: string & !=""
	to:   string & !=""
	allow?: [...string]
}

#AgentTeam: {
	description?: string & !=""
	agents: [#AgentTeamMember, ...#AgentTeamMember]
	edges?: [...#AgentDelegationEdge]
	coordinator?:     string
	concurrency?:     int & >=1
	evidence_policy?: "target" | "coordinator" | "both" @go(EvidencePolicy)
}

// Durable control-plane projection of a declarative team. The authored graph
// remains #AgentTeam; this record binds each member name to the ephemeral
// session created for one dispatch so later delegation can re-check authority.
#AgentTeamRecord: {
	id:         #UUIDv7 @go(ID)
	team:       #AgentTeam
	sessions:   {[string]: #UUIDv7}
	created_at: string @go(CreatedAt)
}

#AgentFederationRecord: {
	id:         #UUIDv7 @go(ID)
	node:       string & !=""
	owner:     string & !=""
	session_id?: #UUIDv7 @go(SessionID)
	run_id?:     #UUIDv7 @go(RunID)
	state:      "delegated" | "active" | "settled" | "failed"
	updated_at: string @go(UpdatedAt)
	metadata?: #StrMap
}

#Incident: {
	id:         #UUIDv7 @go(ID)
	run_id?:    #UUIDv7 @go(RunID)
	state:      "needs_rca" | "rca_active" | "awaiting_recovery" | "resolved"
	summary:    string & !=""
	created_at: string @go(CreatedAt)
	evidence_refs?: [...string] @go(EvidenceRefs)
}

#RCARecord: {
	id:          #UUIDv7 @go(ID)
	incident_id: #UUIDv7 @go(IncidentID)
	state:       "active" | "complete"
	findings?: [...string]
	root_cause?:   string @go(RootCause)
	completed_at?: string @go(CompletedAt)
}

#RecoveryParams: {
	run_id?:           #UUIDv7 @go(RunID)
	session_id?:       #UUIDv7 @go(SessionID)
	target?:           #TargetSpec @go(Target,type=*TargetSpec)
	terminal_profile?: #TerminalProfile @go(TerminalProfile,type=*TerminalProfile)
	provider?:         string & !=""
	prompt?:           string
	runtime?:          string & !=""
	deployment?:       string & !=""
	note?:             string & !=""
}

#RecoveryDecision: {
	id:                          #UUIDv7 @go(ID)
	incident_id:                 #UUIDv7 @go(IncidentID)
	rca_id?:                     #UUIDv7 @go(RCAID)
	action:                      "reattach" | "resume" | "restart" | "rebuild-target" | "change-runtime" | "reassign" | "abort" | "operator"
	authorized_emergency_abort?: bool @go(AuthorizedEmergencyAbort)
	params?:                     #RecoveryParams @go(Params,type=*RecoveryParams)
	state:                       "planned" | "applied" | "failed"
	decided_at:                  string @go(DecidedAt)
	applied_at?:                 string @go(AppliedAt)
	error?:                      string
}

#TerminalProfile: {
	name: string & !=""
	entrypoint: [string, ...string]
	working_dir?: string @go(WorkingDir)
	env?:         #StrMap
	cols:         *120 | (int & >0 & <=1000)
	rows:         *40 | (int & >0 & <=1000)
	readiness?: {...} @go(Readiness,type=map[string]any)
	semantic_adapter?: string @go(SemanticAdapter)
	keys?: [...string]
	signals?: [...string]
	persistence?: "none" | "detach" | "required"
	transcript?:  "none" | "raw" | "screen" | "both"
}

#TerminalInput: {
	kind:    "text" | "paste" | "command" | "key" | "resize" | "signal" | "close"
	text?:   string
	key?:    string
	cols?:   int & >0 & <=1000
	rows?:   int & >0 & <=1000
	signal?: string
}

#TerminalKey: {
	key: string & !=""
}

#TerminalResize: {
	cols: int & >0 & <=1000
	rows: int & >0 & <=1000
}

#TerminalFrame: {
	run_id:     #UUIDv7 @go(RunID)
	sequence:   int & >=1
	kind:       "raw" | "screen" | "status" | "exit" | "error" | "resync"
	stream?:    "stdout" | "stderr" | "terminal"
	data?:      bytes
	snapshot?:  string
	status?:    string
	exit_code?: int @go(ExitCode,type=*int)
	error?:     string
}

#TerminalSnapshot: {
	run_id:      #UUIDv7 @go(RunID)
	sequence:    int & >=0
	cols:        int & >0
	rows:        int & >0
	screen:      string
	cursor_col?: int & >=0 @go(CursorCol)
	cursor_row?: int & >=0 @go(CursorRow)
}

#TerminalExit: {
	run_id:    #UUIDv7 @go(RunID)
	exit_code: int     @go(ExitCode)
	reason?:   string
	time:      string
}
