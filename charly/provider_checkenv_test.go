package main

// provider_checkenv_test.go — the P4 check-env carrier: the deployment's
// mcp_provide declarations must survive snapshotCheckEnv into the wire env so
// the out-of-process mcp: verb can resolve a VM/host MCP endpoint (no
// podman-inspectable OCI label on a VM). Fails without the threading.

import (
	"testing"

	"github.com/opencharly/spec/spec"
)

// TestSnapshotCheckEnvCarriesMCPProvide — the wire env snapshot carries the
// carrier's mcp_provide declarations verbatim (the substrate-neutral resolution
// leg the mcp: verb needs for VM venues).
func TestSnapshotCheckEnvCarriesMCPProvide(t *testing.T) {
	cc := &hostCheckCarrier{
		mode: spec.CheckModeLive,
		box:  "cachyos-vm",
		mcpProvide: []spec.CandyMCPProvide{
			{Name: "charly", URL: "http://127.0.0.1:18765/mcp", Transport: "http"},
		},
	}
	ce := snapshotCheckEnv(cc, nil)
	if len(ce.MCPProvide) != 1 || ce.MCPProvide[0].Name != "charly" || ce.MCPProvide[0].URL != "http://127.0.0.1:18765/mcp" {
		t.Fatalf("snapshotCheckEnv lost the mcp_provide declarations: got %+v", ce.MCPProvide)
	}
	// The existing scalar legs still populate (no regression).
	if ce.Box != "cachyos-vm" || ce.Mode != "live" {
		t.Errorf("snapshotCheckEnv scalars regressed: %+v", ce)
	}
}
