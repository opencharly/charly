package main

import (
	"testing"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

func TestRemoveBySource(t *testing.T) {
	entries := []spec.MCPProvideEntry{
		{Name: "jupyter", URL: "http://charly-jupyter:8888/mcp", Source: "jupyter"},
		{Name: "code-search", URL: "http://charly-search:3100/mcp", Source: "search"},
	}

	got, removed := deploykit.RemoveBySource(entries, "jupyter")
	if !removed {
		t.Error("removeBySource should report removal")
	}
	if len(got) != 1 || got[0].Name != "code-search" {
		t.Errorf("removeBySource = %v, want only code-search", got)
	}

	got2, removed2 := deploykit.RemoveBySource(entries, "nonexistent")
	if removed2 {
		t.Error("removeBySource should not report removal for nonexistent source")
	}
	if len(got2) != 2 {
		t.Errorf("deploykit.RemoveBySource(nonexistent) should keep all entries, got %d", len(got2))
	}
}

func TestAllocateAutoPorts(t *testing.T) {
	containerPorts := []int{2718, 8080, 3000}
	occupied := map[int]bool{}
	result, err := AllocateAutoPorts(containerPorts, occupied)
	if err != nil {
		t.Fatalf("AllocateAutoPorts unexpected error: %v", err)
	}
	if len(result) != 3 {
		t.Fatalf("AllocateAutoPorts: got %d mappings, want 3", len(result))
	}
	for i, m := range result {
		if m.Container != containerPorts[i] {
			t.Errorf("mapping %d: container=%d, want %d", i, m.Container, containerPorts[i])
		}
		if m.Host == 0 || m.Host > 65535 {
			t.Errorf("mapping %d: invalid host port %d", i, m.Host)
		}
		if !occupied[m.Host] {
			t.Errorf("mapping %d: host port %d not recorded in occupied set", i, m.Host)
		}
	}
	// All host ports should be distinct.
	seen := map[int]bool{}
	for _, m := range result {
		if seen[m.Host] {
			t.Errorf("duplicate host port %d in allocation", m.Host)
		}
		seen[m.Host] = true
	}
}

func TestResolveDeployPorts(t *testing.T) {
	// Auto-default: every container port gets a fresh host port; the mapping's
	// container side matches and the host side is a real (>0) allocated port.
	got, err := ResolveDeployPorts([]int{2718, 8080, 3000}, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("ResolveDeployPorts(auto): got %d entries, want 3", len(got))
	}
	for i, cp := range []int{2718, 8080, 3000} {
		pm, ok := ParsePortMapping(got[i])
		if !ok || pm.Container != cp || pm.Host <= 0 {
			t.Errorf("entry %d = %q, want host:%d with a real host port", i, got[i], cp)
		}
	}

	// A pin wins for its container port; the rest auto-allocate.
	got, err = ResolveDeployPorts([]int{2718, 8080}, []string{"28080:8080"}, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 || got[1] != "28080:8080" {
		t.Errorf("pin not honored: %v", got)
	}

	// Prior allocation is reused for stability across re-resolution.
	got, err = ResolveDeployPorts([]int{2718}, nil, []string{"49718:2718"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0] != "49718:2718" {
		t.Errorf("prior not reused: %v, want [49718:2718]", got)
	}

	// A stray "auto" pin token is ignored (treated as no pin → allocate).
	got, err = ResolveDeployPorts([]int{2718}, []string{"auto"}, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("stray auto: got %v, want 1 allocated entry", got)
	}
}

func TestPodAwareEnvProvides(t *testing.T) {
	entries := []deploykit.EnvProvideEntry{
		{Name: "OLLAMA_HOST", Value: "http://charly-combined:11434", Source: "combined-image"},
		{Name: "PGHOST", Value: "charly-postgresql", Source: "postgresql-image"},
	}

	// Pod case: consumer IS the combined-image — own entries resolve to localhost
	got := deploykit.PodAwareEnvProvides(entries, "combined-image", "charly-combined")
	if len(got) != 2 {
		t.Fatalf("podAwareEnvProvides should return 2 entries, got %d", len(got))
	}
	// Local entry should use localhost
	if got[0].Name != "OLLAMA_HOST" || got[0].Value != "http://localhost:11434" {
		t.Errorf("pod-local entry: got %+v, want localhost URL", got[0])
	}
	// Remote entry should keep hostname
	if got[1].Name != "PGHOST" || got[1].Value != "charly-postgresql" {
		t.Errorf("cross-container entry: got %+v, want original value", got[1])
	}
}

func TestPodAwareEnvProvidesLocalPrecedence(t *testing.T) {
	// Both local and remote provide the same env var name
	entries := []deploykit.EnvProvideEntry{
		{Name: "OLLAMA_HOST", Value: "http://charly-combined:11434", Source: "combined-image"},
		{Name: "OLLAMA_HOST", Value: "http://charly-standalone:11434", Source: "standalone"},
	}

	got := deploykit.PodAwareEnvProvides(entries, "combined-image", "charly-combined")
	if len(got) != 1 {
		t.Fatalf("podAwareEnvProvides with name conflict: got %d entries, want 1 (local wins)", len(got))
	}
	if got[0].Value != "http://localhost:11434" {
		t.Errorf("local should win: got Value %q, want localhost", got[0].Value)
	}
}

func TestPodAwareEnvProvidesCrossContainer(t *testing.T) {
	// Consumer is a different image — all entries are remote
	entries := []deploykit.EnvProvideEntry{
		{Name: "OLLAMA_HOST", Value: "http://charly-ollama:11434", Source: "ollama-image"},
	}

	got := deploykit.PodAwareEnvProvides(entries, "hermes-image", "charly-hermes")
	if len(got) != 1 {
		t.Fatalf("cross-container: got %d entries, want 1", len(got))
	}
	if got[0].Value != "http://charly-ollama:11434" {
		t.Errorf("cross-container should keep original value: got %q", got[0].Value)
	}
}

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
