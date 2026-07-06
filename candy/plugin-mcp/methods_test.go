package mcp

import (
	"github.com/opencharly/sdk/spec"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// methods_test.go covers the PLUGIN-side dial/dispatch/format layer that moved
// out-of-process from charly/mcp_client.go. The host-side resolution helpers
// (pickMCPEntry / resolveContainerNameTemplate / rewriteMCPURLForHost) keep their
// tests in charly (mcp_preresolve_test.go).

// ---------------------------------------------------------------------------
// buildMCPTransport — transport dispatch
// ---------------------------------------------------------------------------

func TestBuildMCPTransport(t *testing.T) {
	cases := []struct {
		transport string
		wantType  string // %T substring
		wantErr   bool
	}{
		{"", "StreamableClientTransport", false},
		{"http", "StreamableClientTransport", false},
		{"HTTP", "StreamableClientTransport", false},
		{"streamable-http", "StreamableClientTransport", false},
		{"sse", "SSEClientTransport", false},
		{"SSE", "SSEClientTransport", false},
		{"stdio", "", true},
		{"websocket", "", true},
	}
	for _, tc := range cases {
		tr, err := buildMCPTransport("http://x", tc.transport)
		if tc.wantErr {
			if err == nil {
				t.Errorf("transport=%q: expected error, got %T", tc.transport, tr)
			}
			continue
		}
		if err != nil {
			t.Errorf("transport=%q: unexpected error: %v", tc.transport, err)
			continue
		}
		typeName := typeOf(tr)
		if !strings.Contains(typeName, tc.wantType) {
			t.Errorf("transport=%q: got %s, want contains %s", tc.transport, typeName, tc.wantType)
		}
	}
}

// typeOf returns a short concrete type name for the SDK transport implementations.
func typeOf(v any) string {
	switch v.(type) {
	case *mcp.StreamableClientTransport:
		return "mcp.StreamableClientTransport"
	case *mcp.SSEClientTransport:
		return "mcp.SSEClientTransport"
	}
	return "unknown"
}

// ---------------------------------------------------------------------------
// formatServers — host-resolved entry list rendering
// ---------------------------------------------------------------------------

func TestFormatServers(t *testing.T) {
	got := formatServers([]spec.MCPProvideEntry{
		{Name: "jupyter", URL: "http://x:8888/mcp", Transport: ""},
		{Name: "chrome-devtools", URL: "http://x:9224/mcp", Transport: "sse"},
	})
	want := "jupyter\thttp://x:8888/mcp\thttp\nchrome-devtools\thttp://x:9224/mcp\tsse"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// Formatters — stable one-line-per-record output
// ---------------------------------------------------------------------------

func TestFormatTool(t *testing.T) {
	got := formatTool(&mcp.Tool{Name: "insert_cell", Description: "Insert a cell.\nSecond line."})
	want := "insert_cell\tInsert a cell."
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatTool_Nil(t *testing.T) {
	if got := formatTool(nil); got != "" {
		t.Errorf("expected empty for nil, got %q", got)
	}
}

func TestFormatResource(t *testing.T) {
	got := formatResource(&mcp.Resource{URI: "file:///foo", Name: "foo", MIMEType: "text/plain"})
	want := "file:///foo\tfoo\ttext/plain"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatPrompt(t *testing.T) {
	got := formatPrompt(&mcp.Prompt{Name: "greet", Description: "Greet someone"})
	want := "greet\tGreet someone"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFirstLine(t *testing.T) {
	cases := []struct{ in, want string }{
		{"single", "single"},
		{"first\nsecond", "first"},
		{"\n  \n  useful  \nmore", "useful"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := firstLine(tc.in); got != tc.want {
			t.Errorf("firstLine(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// extractToolText
// ---------------------------------------------------------------------------

func TestExtractToolText(t *testing.T) {
	res := &mcp.CallToolResult{Content: []mcp.Content{
		&mcp.TextContent{Text: "line one"},
		&mcp.TextContent{Text: "line two"},
	}}
	want := "line one\nline two"
	if got := extractToolText(res); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestExtractToolText_Nil(t *testing.T) {
	if got := extractToolText(nil); got != "" {
		t.Errorf("expected empty for nil, got %q", got)
	}
}

func TestExtractToolText_NoContent(t *testing.T) {
	if got := extractToolText(&mcp.CallToolResult{}); got != "" {
		t.Errorf("expected empty for no content, got %q", got)
	}
}
