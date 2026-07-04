package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk/spec"
)

// provider.go is the out-of-process mcp verb provider — charly's host dispatches a
// `mcp:` check step to it through the registry (ResolveVerb("mcp") → this grpcProvider
// → Provider.Invoke) with the FULL #Op marshaled as params_json and a CheckEnv
// snapshot as env. Because the out-of-process path does NOT run a host-side
// matcher pipeline, this Invoke OWNS the whole verdict: read the
// host-pre-resolved MCP context (the host owns the podman / OCI-label / port-mapping
// resolution), dispatch the method (metadata-only for `servers`; dial + MCP protocol
// for the rest), then evaluate the stdout/stderr/exit_status matchers itself (via the
// shared sdk implementation — R3), and return the wire {status,message} the host decodes.

// pluginResult is the wire form a verb provider returns (the host's pluginCheckResult).
type pluginResult struct {
	Status  string `json:"status"` // "pass" | "fail" | "skip"
	Message string `json:"message"`
}

func resultJSON(status, msg string) (*pb.InvokeReply, error) {
	j, err := json.Marshal(pluginResult{Status: status, Message: msg})
	if err != nil {
		return nil, err
	}
	return &pb.InvokeReply{ResultJson: j}, nil
}

// mcpProvideEntry mirrors the host's MCPProvideEntry over the wire (the declared
// mcp_provides list the `servers` method enumerates without dialing).
type mcpProvideEntry struct {
	Name      string `json:"name"`
	URL       string `json:"url"`
	Transport string `json:"transport,omitempty"`
	Source    string `json:"source"`
}

// mcpEndpoint is the plugin-side decode of the host-resolved McpEnv
// (charly/mcp_preresolve.go). Entries carries every declared server (for `servers`);
// URL/Transport/Name carry the single picked, host-routable dial endpoint (for every
// other method). The plugin needs no podman / OCI labels — it just reads this.
type mcpEndpoint struct {
	Entries   []mcpProvideEntry `json:"entries"`
	URL       string            `json:"url"`
	Transport string            `json:"transport"`
	Name      string            `json:"name"`
}

// mcpEnv is the plugin-side decode of the CheckEnv the host ships as Operation.Env for
// a `mcp:` check step (provider_checkenv.go). Box/Mode mirror the shared CheckEnv; Mcp
// carries the host-resolved context (nil when the host could not resolve one — e.g. no
// mcp op, no live deployment).
type mcpEnv struct {
	Box       string          `json:"box"`
	Mode      string          `json:"mode"` // "live" | "box"
	Substrate json.RawMessage `json:"substrate"`
}

type provider struct{ pb.UnimplementedProviderServer }

// Invoke is the gRPC entry point for the ONE gRPC-served capability this plugin advertises:
// verb:mcp (the MCP check verb). command:mcp (`charly mcp …`) is NOT served over gRPC — it is
// dispatched by charly fork/exec'ing this binary in CLI mode (sdk.Main → cliMain, command.go),
// so it never reaches Invoke and is absent from Describe.
func (p provider) Invoke(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	return p.invokeVerb(ctx, req)
}

// invokeVerb runs one `mcp:` check operation. It decodes the full #Op + the env, skips in
// box mode (no live MCP server on a disposable `charly check box`), dispatches the method
// (metadata for `servers`, dial + MCP protocol otherwise), and self-evaluates the
// matchers.
func (p provider) invokeVerb(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	var op spec.Op
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &op); err != nil {
			return resultJSON("fail", "mcp: decode op: "+err.Error())
		}
	}
	var env mcpEnv
	if len(req.GetEnvJson()) > 0 {
		_ = json.Unmarshal(req.GetEnvJson(), &env)
	}
	// The host's verb preresolver ships the resolved MCP context in the opaque
	// CheckEnv.Substrate (the generic per-verb channel that replaced the typed
	// CheckEnv.Mcp field); decode it into the plugin's own endpoint type.
	var ep *mcpEndpoint
	if len(env.Substrate) > 0 {
		var e mcpEndpoint
		if err := json.Unmarshal(env.Substrate, &e); err == nil {
			ep = &e
		}
	}
	method := string(op.Mcp)

	// Live-deployment verb: skip under `charly check box` (no running MCP server on a
	// disposable `podman run --rm`) — mirrors the host's RunModeBox/box-mode skip.
	if env.Mode == "box" {
		return resultJSON("skip", fmt.Sprintf("mcp: %s requires a running deployment (skip under charly check box)", method))
	}
	// No endpoint resolved → skip. The host already FAILs the "no mcp_provides" /
	// resolution-error cases before dispatch, so a nil endpoint here means no live
	// deployment context at all (the analogue of the host's empty-box skip).
	if ep == nil {
		return resultJSON("skip", fmt.Sprintf("mcp: %s has no resolved MCP endpoint (box=%q)", method, env.Box))
	}

	out, runErr := dispatch(ctx, ep, &op)

	// Exit semantics: the in-tree CLI mapped a Run() error → exit 1, success → exit 0;
	// the host compared that against the authored exit_status (default 0).
	exit := 0
	stderr := ""
	if runErr != nil {
		exit = 1
		stderr = runErr.Error()
	}
	wantExit := 0
	if op.ExitStatus != nil {
		wantExit = *op.ExitStatus
	}
	if exit != wantExit {
		return resultJSON("fail", fmt.Sprintf("mcp: %s: exit=%d, want %d (stderr: %s)", method, exit, wantExit, preview(stderr)))
	}

	if err := sdk.MatchAll(out, op.Stdout); err != nil {
		return resultJSON("fail", fmt.Sprintf("mcp: %s: stdout: %v (got: %s)", method, err, preview(out)))
	}
	if err := sdk.MatchAll(stderr, op.Stderr); err != nil {
		return resultJSON("fail", fmt.Sprintf("mcp: %s: stderr: %v (got: %s)", method, err, preview(stderr)))
	}

	body := out
	if strings.TrimSpace(body) == "" {
		body = stderr
	}
	if strings.TrimSpace(body) == "" {
		body = fmt.Sprintf("mcp %s: exit=%d", method, exit)
	}
	return resultJSON("pass", body)
}

// preview trims long output for an error message (mirrors charly's trimPreview).
func preview(s string) string {
	s = strings.TrimSpace(s)
	const max = 400
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}
