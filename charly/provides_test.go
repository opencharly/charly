package main

import (
	"testing"

	"github.com/opencharly/spec/spec"
)

// TestRemoveBySource / TestAllocateAutoPorts / TestResolveDeployPorts /
// TestPodAwareEnvProvides* (4) relocated to candy/plugin-fleet (#55 decoupling, Batch A) —
// they asserted deploykit.RemoveBySource/PodAwareEnvProvides or
// kit.AllocateAutoPorts/ResolveDeployPorts/ParsePortMapping directly, zero charly dep.

func TestPodAwareMCPProvides(t *testing.T) {
	entries := []spec.MCPProvideEntry{
		{Name: "jupyter", URL: "http://charly-combined:8888/mcp", Transport: "http", Source: "combined-image"},
		{Name: "code-search", URL: "http://charly-search:3100/mcp", Transport: "http", Source: "search-image"},
	}

	// Pod case: consumer IS the combined-image — own entries resolve to localhost
	got := spec.PodAwareMCPProvides(entries, "combined-image", "charly-combined")
	if len(got) != 2 {
		t.Fatalf("podAwareMCPProvides should return 2 entries, got %d", len(got))
	}
	// Local entry should use localhost
	if got[0].Name != "jupyter" || got[0].URL != "http://localhost:8888/mcp" {
		t.Errorf("pod-local entry: got %+v, want localhost URL", got[0])
	}
	// Remote entry should keep hostname
	if got[1].Name != "code-search" || got[1].URL != "http://charly-search:3100/mcp" {
		t.Errorf("cross-container entry: got %+v, want original URL", got[1])
	}
}

func TestPodAwareMCPProvidesLocalPrecedence(t *testing.T) {
	// Both local and remote provide the same MCP server name
	entries := []spec.MCPProvideEntry{
		{Name: "jupyter", URL: "http://charly-combined:8888/mcp", Transport: "http", Source: "combined-image"},
		{Name: "jupyter", URL: "http://charly-standalone:8888/mcp", Transport: "http", Source: "standalone"},
	}

	got := spec.PodAwareMCPProvides(entries, "combined-image", "charly-combined")
	if len(got) != 1 {
		t.Fatalf("podAwareMCPProvides with name conflict: got %d entries, want 1 (local wins)", len(got))
	}
	if got[0].URL != "http://localhost:8888/mcp" {
		t.Errorf("local should win: got URL %q, want localhost", got[0].URL)
	}
}

func TestPodAwareMCPProvidesCrossContainer(t *testing.T) {
	// Consumer is a different image — all entries are remote
	entries := []spec.MCPProvideEntry{
		{Name: "jupyter", URL: "http://charly-jupyter:8888/mcp", Transport: "http", Source: "jupyter-image"},
	}

	got := spec.PodAwareMCPProvides(entries, "hermes-image", "charly-hermes")
	if len(got) != 1 {
		t.Fatalf("cross-container: got %d entries, want 1", len(got))
	}
	if got[0].URL != "http://charly-jupyter:8888/mcp" {
		t.Errorf("cross-container should keep original URL: got %q", got[0].URL)
	}
}
