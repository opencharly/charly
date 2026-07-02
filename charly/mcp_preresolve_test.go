package main

import (
	"strings"
	"testing"
)

// mcp_preresolve_test.go covers the HOST-side MCP resolution helpers that stay in
// charly's core (the dial/dispatch/format layer moved out-of-process to
// candy/plugin-mcp, where its own tests live). These three helpers need charly's
// image metadata / container inspection / port-mapping data an out-of-process plugin
// cannot reach, so they remain host-side in mcp_preresolve.go.

// ---------------------------------------------------------------------------
// pickMCPEntry — discriminator semantics
// ---------------------------------------------------------------------------

func TestPickMCPEntry_Empty(t *testing.T) {
	_, err := pickMCPEntry(nil, "")
	if err == nil || !strings.Contains(err.Error(), "no mcp_provides") {
		t.Fatalf("expected no-entries error, got %v", err)
	}
}

func TestPickMCPEntry_SingleAutoPicks(t *testing.T) {
	entries := []MCPProvideEntry{{Name: "jupyter", URL: "http://x:8888/mcp"}}
	got, err := pickMCPEntry(entries, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name != "jupyter" {
		t.Fatalf("expected jupyter, got %q", got.Name)
	}
}

func TestPickMCPEntry_MultipleRequireName(t *testing.T) {
	entries := []MCPProvideEntry{
		{Name: "jupyter", URL: "http://x:8888/mcp"},
		{Name: "chrome-devtools", URL: "http://x:9224/mcp"},
	}
	_, err := pickMCPEntry(entries, "")
	if err == nil || !strings.Contains(err.Error(), "multiple mcp servers") {
		t.Fatalf("expected disambiguation error, got %v", err)
	}
	if !strings.Contains(err.Error(), "jupyter") || !strings.Contains(err.Error(), "chrome-devtools") {
		t.Fatalf("expected error to list available names, got %v", err)
	}
}

func TestPickMCPEntry_NamedMatch(t *testing.T) {
	entries := []MCPProvideEntry{
		{Name: "jupyter", URL: "http://x:8888/mcp"},
		{Name: "chrome-devtools", URL: "http://x:9224/mcp"},
	}
	got, err := pickMCPEntry(entries, "chrome-devtools")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name != "chrome-devtools" {
		t.Fatalf("expected chrome-devtools, got %q", got.Name)
	}
}

func TestPickMCPEntry_UnknownName(t *testing.T) {
	entries := []MCPProvideEntry{{Name: "jupyter", URL: "http://x:8888/mcp"}}
	_, err := pickMCPEntry(entries, "bogus")
	if err == nil || !strings.Contains(err.Error(), `named "bogus"`) {
		t.Fatalf("expected unknown-name error, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// resolveContainerNameTemplate — trivial substitution
// ---------------------------------------------------------------------------

func TestResolveContainerNameTemplate(t *testing.T) {
	cases := []struct {
		raw, ctrName, want string
	}{
		{"http://{{.ContainerName}}:8888/mcp", "charly-jupyter", "http://charly-jupyter:8888/mcp"},
		{"http://x/y", "ignored", "http://x/y"},
		{"", "charly-jupyter", ""},
		{"http://{{.ContainerName}}", "", "http://{{.ContainerName}}"}, // empty ctrName: no-op
	}
	for _, tc := range cases {
		got := resolveContainerNameTemplate(tc.raw, tc.ctrName)
		if got != tc.want {
			t.Errorf("resolve(%q, %q) = %q, want %q", tc.raw, tc.ctrName, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// rewriteMCPURLForHost — the load-bearing translator
// ---------------------------------------------------------------------------

func makeInspect(hostPort string) *ContainerInspection {
	return &ContainerInspection{
		NetworkSettings: InspectNetwork{
			Ports: map[string][]InspectPortBind{
				"8888/tcp": {{HostPort: hostPort}},
			},
		},
	}
}

func TestRewriteMCPURL_ContainerName(t *testing.T) {
	got, err := rewriteMCPURLForHost("http://charly-jupyter:8888/mcp", "charly-jupyter", makeInspect("8888"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "http://127.0.0.1:8888/mcp"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRewriteMCPURL_RemappedHostPort(t *testing.T) {
	// Container port 8888 is published to host port 18888 (instance remap).
	got, err := rewriteMCPURLForHost("http://charly-jupyter:8888/mcp", "charly-jupyter", makeInspect("18888"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "http://127.0.0.1:18888/mcp"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRewriteMCPURL_ExternalHostPassthrough(t *testing.T) {
	// URL points at an external host — do not rewrite.
	got, err := rewriteMCPURLForHost("https://mcp.example.com/api", "charly-jupyter", makeInspect("8888"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "https://mcp.example.com/api" {
		t.Errorf("expected passthrough, got %q", got)
	}
}

func TestRewriteMCPURL_NoHostPort(t *testing.T) {
	// Container declares port 8888 but nothing is published.
	inspect := &ContainerInspection{
		NetworkSettings: InspectNetwork{Ports: map[string][]InspectPortBind{"8888/tcp": nil}},
	}
	_, err := rewriteMCPURLForHost("http://charly-jupyter:8888/mcp", "charly-jupyter", inspect)
	if err == nil || !strings.Contains(err.Error(), "not published to a host port") {
		t.Fatalf("expected unpublished-port error, got %v", err)
	}
}

func TestRewriteMCPURL_NoInspection(t *testing.T) {
	_, err := rewriteMCPURLForHost("http://charly-jupyter:8888/mcp", "charly-jupyter", nil)
	if err == nil || !strings.Contains(err.Error(), "no container inspection") {
		t.Fatalf("expected no-inspection error, got %v", err)
	}
}

func TestRewriteMCPURL_MissingPort(t *testing.T) {
	// URL has no explicit port. Can't map.
	_, err := rewriteMCPURLForHost("http://charly-jupyter/mcp", "charly-jupyter", makeInspect("8888"))
	if err == nil || !strings.Contains(err.Error(), "no port") {
		t.Fatalf("expected missing-port error, got %v", err)
	}
}

func TestRewriteMCPURL_LocalhostIsAccepted(t *testing.T) {
	// Entry may already be pod-rewritten to localhost from podAwareMCPProvides;
	// we still need to map the container port to the published host port.
	got, err := rewriteMCPURLForHost("http://localhost:8888/mcp", "charly-jupyter", makeInspect("18888"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "http://127.0.0.1:18888/mcp"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
