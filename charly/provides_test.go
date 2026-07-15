package main

import (
	"reflect"
	"testing"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

func TestFilterOwnProvidesEnv(t *testing.T) {
	entries := []EnvProvideEntry{
		{Name: "OLLAMA_HOST", Value: "http://charly-ollama:11434", Source: "ollama"},
		{Name: "PGHOST", Value: "charly-postgresql", Source: "postgresql"},
		{Name: "CUSTOM", Value: "val", Source: "myimage"},
	}

	got := filterOwnProvides(entries, "ollama")
	want := []EnvProvideEntry{
		{Name: "PGHOST", Value: "charly-postgresql", Source: "postgresql"},
		{Name: "CUSTOM", Value: "val", Source: "myimage"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("filterOwnProvides(env, ollama) = %v, want %v", got, want)
	}
}

func TestFilterOwnProvidesMCP(t *testing.T) {
	entries := []MCPProvideEntry{
		{Name: "jupyter", URL: "http://charly-jupyter:8888/mcp", Transport: "http", Source: "jupyter"},
		{Name: "code-search", URL: "http://charly-search:3100/mcp", Transport: "http", Source: "search"},
	}

	got := filterOwnProvides(entries, "jupyter")
	if len(got) != 1 || got[0].Name != "code-search" {
		t.Errorf("filterOwnProvides(mcp, jupyter) = %v, want only code-search", got)
	}
}

func TestFilterOwnProvidesEmpty(t *testing.T) {
	entries := []MCPProvideEntry{
		{Name: "test", URL: "http://localhost", Source: "img"},
	}
	got := filterOwnProvides(entries, "")
	if len(got) != 1 {
		t.Errorf("filterOwnProvides with empty boxName should return all entries")
	}
}

func TestRemoveBySource(t *testing.T) {
	entries := []MCPProvideEntry{
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

func TestResolveTemplate(t *testing.T) {
	tests := []struct {
		tmpl, ctr, want string
		portMap         map[int]int
	}{
		{tmpl: "http://{{.ContainerName}}:8888/mcp", ctr: "charly-jupyter", want: "http://charly-jupyter:8888/mcp"},
		{tmpl: "no-template", ctr: "charly-test", want: "no-template"},
		{tmpl: "{{.ContainerName}}:{{.ContainerName}}", ctr: "charly-x", want: "charly-x:charly-x"},
		// New: {{.HostPort N}} resolves against portMap.
		{tmpl: "http://127.0.0.1:{{.HostPort 3000}}", ctr: "charly-versa",
			portMap: map[int]int{3000: 23000}, want: "http://127.0.0.1:23000"},
		// New: unmapped container port falls back to literal N.
		{tmpl: "http://127.0.0.1:{{.HostPort 9999}}", ctr: "charly-versa",
			portMap: map[int]int{3000: 23000}, want: "http://127.0.0.1:9999"},
		// New: nil portMap → fallback to literal N.
		{tmpl: "http://127.0.0.1:{{.HostPort 8080}}", ctr: "charly-x",
			portMap: nil, want: "http://127.0.0.1:8080"},
		// New: {{.ContainerPort N}} always resolves to N (symmetry/readability).
		{tmpl: "http://{{.ContainerName}}:{{.ContainerPort 8080}}", ctr: "charly-airflow",
			portMap: map[int]int{8080: 28080}, want: "http://charly-airflow:8080"},
		// Combined: both placeholders + container name.
		{tmpl: "internal=http://{{.ContainerName}}:{{.ContainerPort 8080}} public=http://127.0.0.1:{{.HostPort 8080}}",
			ctr: "charly-airflow", portMap: map[int]int{8080: 28080},
			want: "internal=http://charly-airflow:8080 public=http://127.0.0.1:28080"},
	}
	for _, tt := range tests {
		got := resolveTemplate(tt.tmpl, tt.ctr, tt.portMap)
		if got != tt.want {
			t.Errorf("resolveTemplate(%q, %q, %v) = %q, want %q", tt.tmpl, tt.ctr, tt.portMap, got, tt.want)
		}
	}
}

func TestValidateProvidesTemplate(t *testing.T) {
	tests := []struct {
		tmpl string
		want bool
	}{
		{"http://{{.ContainerName}}:8888/mcp", true},
		{"no-template", true},
		{"{{.ContainerName}}", true},
		{"{{.BadVar}}", false},
		{"{{.ContainerName}}{{.Other}}", false},
		{"{{broken", false},
		// New placeholders — must be allowed when N is numeric.
		{"http://127.0.0.1:{{.HostPort 3000}}", true},
		{"{{.ContainerPort 8080}}", true},
		{"both {{.HostPort 1}} and {{.ContainerPort 2}}", true},
		// Numeric requirement: non-numeric argument is rejected.
		{"{{.HostPort foo}}", false},
		{"{{.ContainerPort bar}}", false},
		// Unterminated placeholders still rejected.
		{"{{.HostPort 3000", false},
	}
	for _, tt := range tests {
		got := validateProvidesTemplate(tt.tmpl)
		if got != tt.want {
			t.Errorf("validateProvidesTemplate(%q) = %v, want %v", tt.tmpl, got, tt.want)
		}
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

func TestPortMapFromMappings(t *testing.T) {
	mappings := []string{"22718:2718", "28080:8080", "127.0.0.1:23000:3000"}
	m := PortMapFromMappings(mappings)
	if m[2718] != 22718 {
		t.Errorf("portMap[2718] = %d, want 22718", m[2718])
	}
	if m[8080] != 28080 {
		t.Errorf("portMap[8080] = %d, want 28080", m[8080])
	}
	if m[3000] != 23000 {
		t.Errorf("portMap[3000] = %d, want 23000 (IP:H:C form)", m[3000])
	}
}

func TestPodAwareEnvProvides(t *testing.T) {
	entries := []EnvProvideEntry{
		{Name: "OLLAMA_HOST", Value: "http://charly-combined:11434", Source: "combined-image"},
		{Name: "PGHOST", Value: "charly-postgresql", Source: "postgresql-image"},
	}

	// Pod case: consumer IS the combined-image — own entries resolve to localhost
	got := podAwareEnvProvides(entries, "combined-image", "charly-combined")
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
	entries := []EnvProvideEntry{
		{Name: "OLLAMA_HOST", Value: "http://charly-combined:11434", Source: "combined-image"},
		{Name: "OLLAMA_HOST", Value: "http://charly-standalone:11434", Source: "standalone"},
	}

	got := podAwareEnvProvides(entries, "combined-image", "charly-combined")
	if len(got) != 1 {
		t.Fatalf("podAwareEnvProvides with name conflict: got %d entries, want 1 (local wins)", len(got))
	}
	if got[0].Value != "http://localhost:11434" {
		t.Errorf("local should win: got Value %q, want localhost", got[0].Value)
	}
}

func TestPodAwareEnvProvidesCrossContainer(t *testing.T) {
	// Consumer is a different image — all entries are remote
	entries := []EnvProvideEntry{
		{Name: "OLLAMA_HOST", Value: "http://charly-ollama:11434", Source: "ollama-image"},
	}

	got := podAwareEnvProvides(entries, "hermes-image", "charly-hermes")
	if len(got) != 1 {
		t.Fatalf("cross-container: got %d entries, want 1", len(got))
	}
	if got[0].Value != "http://charly-ollama:11434" {
		t.Errorf("cross-container should keep original value: got %q", got[0].Value)
	}
}

func TestPodAwareMCPProvides(t *testing.T) {
	entries := []MCPProvideEntry{
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
	entries := []MCPProvideEntry{
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
	entries := []MCPProvideEntry{
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
