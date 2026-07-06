package mcp

import (
	"context"
	"errors"
	"testing"

	"github.com/opencharly/sdk/spec"
)

// pickMcpEntry — discriminator semantics (ported from charly's mcp_preresolve_test.go when the
// pick/template/URL-rewrite logic relocated into the plugin, H part 2).

func TestPickMcpEntry_Empty(t *testing.T) {
	if _, err := pickMcpEntry(nil, ""); err == nil {
		t.Fatal("expected error for empty entries")
	}
}

func TestPickMcpEntry_SingleAutoPicks(t *testing.T) {
	entries := []spec.MCPProvideEntry{{Name: "a", URL: "http://x:1/mcp"}}
	got, err := pickMcpEntry(entries, "")
	if err != nil || got.Name != "a" {
		t.Fatalf("single auto-pick: got %+v err %v", got, err)
	}
}

func TestPickMcpEntry_MultipleRequireName(t *testing.T) {
	entries := []spec.MCPProvideEntry{{Name: "a"}, {Name: "b"}}
	if _, err := pickMcpEntry(entries, ""); err == nil {
		t.Fatal("expected error requiring mcp_name for multiple entries")
	}
}

func TestPickMcpEntry_NamedMatch(t *testing.T) {
	entries := []spec.MCPProvideEntry{{Name: "chrome-devtools"}, {Name: "other"}}
	got, err := pickMcpEntry(entries, "chrome-devtools")
	if err != nil || got.Name != "chrome-devtools" {
		t.Fatalf("named match: got %+v err %v", got, err)
	}
}

func TestPickMcpEntry_UnknownName(t *testing.T) {
	entries := []spec.MCPProvideEntry{{Name: "a"}}
	if _, err := pickMcpEntry(entries, "bogus"); err == nil {
		t.Fatal("expected error for unknown mcp_name")
	}
}

func TestResolveContainerNameTemplate(t *testing.T) {
	cases := []struct{ raw, ctrName, want string }{
		{"http://{{.ContainerName}}:8888/mcp", "charly-jupyter", "http://charly-jupyter:8888/mcp"},
		{"http://static-host:8888/mcp", "charly-jupyter", "http://static-host:8888/mcp"},
		{"http://{{.ContainerName}}:8888/mcp", "", "http://{{.ContainerName}}:8888/mcp"},
	}
	for _, tc := range cases {
		if got := resolveContainerNameTemplate(tc.raw, tc.ctrName); got != tc.want {
			t.Errorf("template(%q,%q) = %q, want %q", tc.raw, tc.ctrName, got, tc.want)
		}
	}
}

// rewriteURLViaEndpoint — the load-bearing translator (now via the ResolveEndpoint func).

func fixedEndpoint(addr string) func(context.Context, int) (string, error) {
	return func(context.Context, int) (string, error) { return addr, nil }
}

func TestRewriteURL_ContainerName(t *testing.T) {
	got, err := rewriteURLViaEndpoint(context.Background(), fixedEndpoint("127.0.0.1:8888"), "http://charly-jupyter:8888/mcp", "charly-jupyter")
	if err != nil || got != "http://127.0.0.1:8888/mcp" {
		t.Fatalf("container-name rewrite: got %q err %v", got, err)
	}
}

func TestRewriteURL_RemappedHostPort(t *testing.T) {
	got, err := rewriteURLViaEndpoint(context.Background(), fixedEndpoint("127.0.0.1:18888"), "http://charly-jupyter:8888/mcp", "charly-jupyter")
	if err != nil || got != "http://127.0.0.1:18888/mcp" {
		t.Fatalf("remapped host port: got %q err %v", got, err)
	}
}

func TestRewriteURL_ExternalHostPassthrough(t *testing.T) {
	got, err := rewriteURLViaEndpoint(context.Background(), fixedEndpoint("127.0.0.1:8888"), "https://mcp.example.com/api", "charly-jupyter")
	if err != nil || got != "https://mcp.example.com/api" {
		t.Fatalf("external passthrough: got %q err %v", got, err)
	}
}

func TestRewriteURL_NoHostPort(t *testing.T) {
	if _, err := rewriteURLViaEndpoint(context.Background(), fixedEndpoint(""), "http://charly-jupyter:8888/mcp", "charly-jupyter"); err == nil {
		t.Fatal("expected error when the port is not published (empty addr)")
	}
}

func TestRewriteURL_EndpointError(t *testing.T) {
	failing := func(context.Context, int) (string, error) { return "", errors.New("no venue") }
	if _, err := rewriteURLViaEndpoint(context.Background(), failing, "http://charly-jupyter:8888/mcp", "charly-jupyter"); err == nil {
		t.Fatal("expected the ResolveEndpoint error to propagate")
	}
}
