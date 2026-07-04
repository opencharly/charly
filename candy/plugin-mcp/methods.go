package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/opencharly/sdk/spec"
)

// methods.go is the mcp method dispatcher + the MCP-protocol client layer, moved from
// charly/mcp.go + charly/mcp_client.go. The 7-method surface
// (ping/servers/list-tools/list-resources/list-prompts/call/read) was refactored from
// CLI Run() methods that PRINTED to stdout into functions that RETURN the captured
// output string — so provider.go can feed the output through the shared sdk matcher
// pipeline (a host-side matcher step does not run for an out-of-process
// verb). The transport dispatch, the SDK connect, and the tab-separated formatters
// (formatTool/formatResource/formatPrompt/firstLine/extractToolText) are unchanged, so
// a bed authored against the in-tree verb passes unchanged. `servers` is metadata-only
// — it never dials, formatting the host-resolved entry list directly.

const defaultMcpTimeout = 30 * time.Second

// requiredModifiers mirrors the in-tree mcpMethods required-field specs (the host's
// validate-time + runtime required-modifier check keyed off the former in-proc
// live-verb seam, which an external verb is not — so the check moves HERE, at
// dispatch). call needs a tool name; read needs a resource URI.
var requiredModifiers = map[string][]string{
	"call": {"tool"},
	"read": {"uri"},
}

func modifierZero(op *spec.Op, name string) bool {
	switch name {
	case "tool":
		return op.Tool == ""
	case "uri":
		return op.URI == ""
	}
	return false
}

func checkRequiredModifiers(method string, op *spec.Op) error {
	var missing []string
	for _, f := range requiredModifiers[method] {
		if modifierZero(op, f) {
			missing = append(missing, f)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("missing required modifier(s): %s", strings.Join(missing, ", "))
}

// dispatch runs one mcp method against the host-resolved endpoint and returns its
// captured output. A returned error is the verb FAILING (the in-tree CLI Run()
// returning an error → exit 1); provider.go maps it through the exit_status / stderr
// matchers. `servers` is metadata-only; every other method dials via the MCP client.
func dispatch(ctx context.Context, ep *mcpEndpoint, op *spec.Op) (string, error) {
	method := string(op.Mcp)
	if err := checkRequiredModifiers(method, op); err != nil {
		return "", err
	}

	// servers: pure metadata — no dial.
	if method == "servers" {
		return formatServers(ep.Entries), nil
	}

	timeout := defaultMcpTimeout
	if op.Timeout != "" {
		if d, err := time.ParseDuration(op.Timeout); err == nil && d > 0 {
			timeout = d
		}
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	sess, closeFn, err := openSession(cctx, ep)
	if err != nil {
		return "", err
	}
	defer closeFn()

	switch method {
	case "ping":
		return runPing(cctx, sess)
	case "list-tools":
		return runListTools(cctx, sess)
	case "list-resources":
		return runListResources(cctx, sess)
	case "list-prompts":
		return runListPrompts(cctx, sess)
	case "call":
		return runCall(cctx, sess, op)
	case "read":
		return runRead(cctx, sess, op)
	}
	return "", fmt.Errorf("unknown mcp method %q", method)
}

// openSession dials the host-pre-resolved endpoint and connects an MCP client session.
func openSession(ctx context.Context, ep *mcpEndpoint) (*mcp.ClientSession, func(), error) {
	transport, err := buildMCPTransport(ep.URL, ep.Transport)
	if err != nil {
		return nil, func() {}, err
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "charly", Version: "plugin-mcp"}, nil)
	sess, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, func() {}, fmt.Errorf("mcp connect %s (%s): %w", ep.Name, ep.URL, err)
	}
	return sess, func() { _ = sess.Close() }, nil
}

// runPing checks liveness and returns "ok".
func runPing(ctx context.Context, sess *mcp.ClientSession) (string, error) {
	if err := sess.Ping(ctx, nil); err != nil {
		return "", fmt.Errorf("mcp ping: %w", err)
	}
	return "ok", nil
}

// runListTools paginates the server's tools and returns one tab-separated line each.
func runListTools(ctx context.Context, sess *mcp.ClientSession) (string, error) {
	var all []*mcp.Tool
	var cursor string
	for {
		res, err := sess.ListTools(ctx, &mcp.ListToolsParams{Cursor: cursor})
		if err != nil {
			return "", fmt.Errorf("mcp list-tools: %w", err)
		}
		all = append(all, res.Tools...)
		if res.NextCursor == "" {
			break
		}
		cursor = res.NextCursor
	}
	lines := make([]string, 0, len(all))
	for _, t := range all {
		lines = append(lines, formatTool(t))
	}
	return strings.Join(lines, "\n"), nil
}

// runListResources paginates the server's resources and returns one line each.
func runListResources(ctx context.Context, sess *mcp.ClientSession) (string, error) {
	var all []*mcp.Resource
	var cursor string
	for {
		res, err := sess.ListResources(ctx, &mcp.ListResourcesParams{Cursor: cursor})
		if err != nil {
			return "", fmt.Errorf("mcp list-resources: %w", err)
		}
		all = append(all, res.Resources...)
		if res.NextCursor == "" {
			break
		}
		cursor = res.NextCursor
	}
	lines := make([]string, 0, len(all))
	for _, r := range all {
		lines = append(lines, formatResource(r))
	}
	return strings.Join(lines, "\n"), nil
}

// runListPrompts paginates the server's prompts and returns one line each.
func runListPrompts(ctx context.Context, sess *mcp.ClientSession) (string, error) {
	var all []*mcp.Prompt
	var cursor string
	for {
		res, err := sess.ListPrompts(ctx, &mcp.ListPromptsParams{Cursor: cursor})
		if err != nil {
			return "", fmt.Errorf("mcp list-prompts: %w", err)
		}
		all = append(all, res.Prompts...)
		if res.NextCursor == "" {
			break
		}
		cursor = res.NextCursor
	}
	lines := make([]string, 0, len(all))
	for _, p := range all {
		lines = append(lines, formatPrompt(p))
	}
	return strings.Join(lines, "\n"), nil
}

// runCall invokes a tool. It returns the concatenated TextContent, and an error when
// the tool reports IsError — so the host's exit_status: 1 assertion can target the
// intentional-error path (the in-tree CLI returned exit 1 on IsError).
func runCall(ctx context.Context, sess *mcp.ClientSession, op *spec.Op) (string, error) {
	var args any
	if strings.TrimSpace(op.Input) != "" {
		if err := json.Unmarshal([]byte(op.Input), &args); err != nil {
			return "", fmt.Errorf("parsing tool arguments as JSON: %w", err)
		}
	}
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: op.Tool, Arguments: args})
	if err != nil {
		return "", fmt.Errorf("mcp call %s: %w", op.Tool, err)
	}
	text := extractToolText(res)
	if res.IsError {
		// Side-effect failures surface as a non-zero exit so declarative tests can
		// assert `exit_status: 1` on intentional-error paths.
		return text, fmt.Errorf("tool %s reported an error", op.Tool)
	}
	return text, nil
}

// runRead reads a resource and returns its text bodies (one per content), with a
// bracketed placeholder for binary blobs so the author sees they happened.
func runRead(ctx context.Context, sess *mcp.ClientSession, op *spec.Op) (string, error) {
	res, err := sess.ReadResource(ctx, &mcp.ReadResourceParams{URI: op.URI})
	if err != nil {
		return "", fmt.Errorf("mcp read %s: %w", op.URI, err)
	}
	var lines []string
	for _, cnt := range res.Contents {
		if cnt == nil {
			continue
		}
		if cnt.Text != "" {
			lines = append(lines, cnt.Text)
			continue
		}
		if len(cnt.Blob) > 0 {
			lines = append(lines, fmt.Sprintf("[binary resource %s: %d bytes, mime %s]", cnt.URI, len(cnt.Blob), cnt.MIMEType))
		}
	}
	return strings.Join(lines, "\n"), nil
}

// ---------------------------------------------------------------------------
// Transport dispatch + formatters (ported verbatim from charly/mcp_client.go).
// ---------------------------------------------------------------------------

// formatServers renders the host-resolved mcp_provides entries as tab-separated
// <name>\t<url>\t<transport> lines (mirroring the in-tree `mcp servers` plaintext).
func formatServers(entries []mcpProvideEntry) string {
	lines := make([]string, 0, len(entries))
	for _, e := range entries {
		transport := e.Transport
		if transport == "" {
			transport = "http"
		}
		lines = append(lines, fmt.Sprintf("%s\t%s\t%s", e.Name, e.URL, transport))
	}
	return strings.Join(lines, "\n")
}

// buildMCPTransport constructs the SDK transport for a resolved endpoint. The
// transport field is advisory ("http" / "sse" / ""); empty and "http" both map to
// Streamable HTTP (the opencharly default and what both the jupyter and
// chrome-devtools MCP servers expose).
func buildMCPTransport(endpoint, transport string) (mcp.Transport, error) {
	switch strings.ToLower(transport) {
	case "", "http", "streamable", "streamable-http":
		return &mcp.StreamableClientTransport{Endpoint: endpoint}, nil
	case "sse":
		return &mcp.SSEClientTransport{Endpoint: endpoint}, nil
	default:
		return nil, fmt.Errorf("unsupported mcp transport %q (expected http or sse)", transport)
	}
}

// formatTool renders a *mcp.Tool as a single tab-separated line so author-facing
// matchers can `contains: insert_cell` without having to decode JSON. Multi-line
// descriptions collapse to the first non-empty line.
func formatTool(t *mcp.Tool) string {
	if t == nil {
		return ""
	}
	return t.Name + "\t" + firstLine(t.Description)
}

// formatResource renders a *mcp.Resource as <uri>\t<name>\t<mime>.
func formatResource(r *mcp.Resource) string {
	if r == nil {
		return ""
	}
	return r.URI + "\t" + r.Name + "\t" + r.MIMEType
}

// formatPrompt renders a *mcp.Prompt as <name>\t<description>.
func formatPrompt(p *mcp.Prompt) string {
	if p == nil {
		return ""
	}
	return p.Name + "\t" + firstLine(p.Description)
}

// firstLine returns the first non-empty line of s with trailing whitespace stripped.
// Used to keep the one-line-per-record output shape even when server-reported
// descriptions span multiple lines.
func firstLine(s string) string {
	for line := range strings.SplitSeq(s, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// extractToolText collects the textual portion of a CallToolResult, one line per
// TextContent block. Non-text content (images, resource links) is summarised with a
// bracketed placeholder so the author sees it happened without trying to print binary
// data.
func extractToolText(res *mcp.CallToolResult) string {
	if res == nil {
		return ""
	}
	var b strings.Builder
	for i, c := range res.Content {
		if i > 0 {
			b.WriteByte('\n')
		}
		switch v := c.(type) {
		case *mcp.TextContent:
			b.WriteString(v.Text)
		case *mcp.ImageContent:
			fmt.Fprintf(&b, "[image content: %s, %d bytes]", v.MIMEType, len(v.Data))
		case *mcp.AudioContent:
			fmt.Fprintf(&b, "[audio content: %s, %d bytes]", v.MIMEType, len(v.Data))
		default:
			fmt.Fprintf(&b, "[non-text content: %T]", c)
		}
	}
	return b.String()
}
