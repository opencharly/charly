// Pi owns the RPC semantics and implementation. This version-pinned boundary
// schema owns every JSONL field Charly accepts or emits while delegating those
// messages unchanged to upstream runRpcMode.
#PiPlugin: {
	runtime:  "pi"
	contract: "pi-rpc-0.80.10"
}

#PiRPCCommand: {
	id?: string & !=""
	type: "prompt" | "steer" | "follow_up" | "abort" | "new_session" |
		"get_state" | "set_model" | "cycle_model" | "get_available_models" |
		"set_thinking_level" | "cycle_thinking_level" | "set_steering_mode" |
		"set_follow_up_mode" | "compact" | "set_auto_compaction" |
		"set_auto_retry" | "abort_retry" | "bash" | "abort_bash" |
		"get_session_stats" | "export_html" | "switch_session" | "fork" |
		"clone" | "get_fork_messages" | "get_entries" | "get_tree" |
		"get_last_assistant_text" | "set_session_name" | "get_messages" |
		"get_commands" | "extension_ui_response"
	message?:               string
	images?: [..._]
	streamingBehavior?:     "steer" | "followUp"
	parentSession?:         string
	provider?:              string & !=""
	modelId?:               string & !=""
	level?:                 string & !=""
	mode?:                  "all" | "one-at-a-time"
	customInstructions?:    string
	enabled?:               bool
	command?:               string
	excludeFromContext?:    bool
	outputPath?:            string
	sessionPath?:           string & !=""
	entryId?:               string & !=""
	since?:                 string
	name?:                  string & !=""
	value?:                 string
	confirmed?:             bool
	cancelled?:             bool
}

#PiGetStateCommand: {
	id:   string & !=""
	type: "get_state"
}

#PiPromptCommand: {
	id:      string & !=""
	type:    "prompt"
	message: string & !=""
}

#PiAbortCommand: {
	type: "abort"
}

// Events are passed through losslessly. The discriminator and the response
// fields Charly interprets are closed and typed; event-specific upstream data
// remains opaque to the adapter.
#PiRPCEvent: {
	type:     string & !=""
	id?:      string
	command?: string
	success?: bool
	error?:   string
	data?:    _
	...
}

#PiRPCState: {
	model?:                  _
	thinkingLevel:           string & !=""
	isStreaming:             bool
	isCompacting:            bool
	steeringMode:            "all" | "one-at-a-time"
	followUpMode:            "all" | "one-at-a-time"
	sessionFile?:            string @go(SessionFile)
	sessionId:               string & !=""
	sessionName?:            string
	autoCompactionEnabled:   bool
	messageCount:            int & >=0
	pendingMessageCount:     int & >=0
}

#PiRPCStateResponse: {
	id?:     string
	type:    "response"
	command: "get_state"
	success: bool
	data?:   #PiRPCState @go(Data,type=*PiRPCState)
	error?:  string
}
