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
// version) and commands contributed via CommandProviders (alias.list; `secrets` is an
// EXTERNAL command now — candy/plugin-secrets — so it is absent from this builtin model, as
// are the C15-externalized clean/settings/candy — see TestCommandProviders_ExtractedReachMCP.
// `version` stays a CORE command — pkg/arch's pkgver() stamps the package version via it — so it
// remains present here).
func TestCLIModel_CoversCommands(t *testing.T) {
	paths := cliModelLeafPaths(t)
	for _, want := range []string{"box.build", "alias.list", "version"} {
		if !paths[want] {
			t.Errorf("CLI model missing leaf %q", want)
		}
	}
}
