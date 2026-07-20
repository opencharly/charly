package main

import "testing"

// cliModelLeafPaths builds the host CLI model (the `charly __cli-model` seam) and returns the
// set of leaf command paths. It REPLACES the deleted mcp_server_test.go `toolIndex` helper:
// the MCP tool surface is now built OUT of process by candy/plugin-mcp FROM this model, so the
// in-core assertion is "the command appears in the reflected CLI model", not "as an MCP tool".
func cliModelLeafPaths(t *testing.T) map[string]bool {
	t.Helper()
	m, err := buildCLIModel()
	if err != nil {
		t.Fatalf("buildCLIModel: %v", err)
	}
	out := make(map[string]bool, len(m.Leaves))
	for _, l := range m.Leaves {
		out[l.Path] = true
	}
	return out
}

// TestCLIModel_CoversCommands proves the CLI-export seam enumerates the command tree the
// out-of-process MCP bridge reflects into tools — both hardcoded machinery (box.build,
// version) and commands contributed via CommandProviders (ssh.tunnel.spice; `secrets` is an
// EXTERNAL command now — candy/plugin-secrets — so it is absent from this builtin model, as
// are the C15-externalized clean/settings/candy and the P14 command:alias — candy/plugin-alias —
// see TestCommandProviders_ExtractedReachMCP. `version` stays a CORE command — pkg/arch's pkgver()
// stamps the package version via it — so it remains present here).
func TestCLIModel_CoversCommands(t *testing.T) {
	paths := cliModelLeafPaths(t)
	for _, want := range []string{"box.build", "ssh.tunnel.spice", "version"} {
		if !paths[want] {
			t.Errorf("CLI model missing leaf %q", want)
		}
	}
}

func TestCLIModel_CoversAgentControlPlane(t *testing.T) {
	m, err := buildCLIModel()
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]bool{}
	for _, leaf := range m.Leaves {
		paths[leaf.Path] = true
	}
	for _, want := range []string{"agent.runtime.list", "agent.runtime.status", "agent.session.new", "agent.session.show", "agent.run.start", "agent.run.list", "agent.run.show", "agent.run.abort", "agent.followup", "agent.steer", "agent.dispatch", "agent.delegate", "agent.team.list", "agent.federation.run", "agent.terminal.launch", "agent.terminal.snapshot", "agent.terminal.transcript", "agent.terminal.input", "agent.terminal.key", "agent.terminal.resize", "agent.terminal.signal", "agent.terminal.close", "agent.incident.create", "agent.incident.show", "agent.rca.show", "agent.rca.complete", "agent.recover.plan", "agent.recover.apply"} {
		if !paths[want] {
			t.Errorf("CLI model missing %s", want)
		}
	}
}
