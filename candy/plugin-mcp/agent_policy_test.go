package mcp

import "testing"

func TestAgentMCPMutationPolicyIsConservative(t *testing.T) {
	for _, path := range []string{"agent.session.create", "agent.session.new", "agent.run.start", "agent.run.abort", "agent.followup", "agent.steer", "agent.dispatch", "agent.delegate", "agent.federation.run", "agent.terminal.run", "agent.terminal.launch", "agent.terminal.input", "agent.terminal.key", "agent.terminal.resize", "agent.terminal.signal", "agent.terminal.close", "agent.incident.create", "agent.rca.complete", "agent.recover.decide", "agent.recover.apply"} {
		if !mcpDestructivePaths[path] {
			t.Errorf("%s is not marked destructive", path)
		}
	}
	for _, path := range []string{"tmux.run", "tmux.cmd", "tmux.send", "tmux.kill"} {
		if !mcpDestructivePaths[path] {
			t.Errorf("%s compatibility mutation is not marked destructive", path)
		}
	}
	for _, path := range []string{"agent.runtime.list", "agent.runtime.status", "agent.session.list", "agent.session.get", "agent.session.show", "agent.run.list", "agent.run.show", "agent.run.events", "agent.team.list", "agent.federation.list", "agent.terminal.snapshot", "agent.incident.list", "agent.incident.show", "agent.rca.list", "agent.rca.show", "agent.recover.list", "agent.recover.show", "agent.recover.plan"} {
		if mcpDestructivePaths[path] {
			t.Errorf("%s should remain read-only", path)
		}
	}
	for _, path := range []string{"agent.terminal.attach", "tui", "tmux.attach", "tmux.shell"} {
		if !mcpSkipToolPaths[path] {
			t.Errorf("%s must not be exposed as an MCP tool", path)
		}
	}
}
