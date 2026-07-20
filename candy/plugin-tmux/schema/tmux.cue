// The tmux plugin's self-contained declaration contract. Terminal and agent
// channel payloads use sdk/schema/agent_control.cue directly; this plugin does
// not duplicate those wire shapes.
#TmuxPlugin: {
	providers: ["terminal:tmux", "agent-runtime:tmux"]
	contract:  "sdk-terminal-profile-v2"
}
