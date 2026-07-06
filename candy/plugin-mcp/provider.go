package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/charly/candy/plugin-mcp/params"
	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/kit"
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

// mcpEndpoint is the mcp check context the plugin builds from the reverse-legs (resolve.go).
// Entries carries every declared server (for `servers`); URL/Transport/Name carry the single
// picked, host-routable dial endpoint (for every other method).
type mcpEndpoint struct {
	Entries   []spec.MCPProvideEntry
	URL       string
	Transport string
	Name      string
}

// mcpEnv is the plugin-side decode of the CheckEnv the host ships as Operation.Env for a `mcp:`
// check step (provider_checkenv.go). Box/Mode mirror the shared CheckEnv; ContainerName is the
// host-authoritative running container name (for the {{.ContainerName}} template + pod-aware
// rewrite). The endpoint is no longer pre-shipped — the plugin resolves it via the reverse-legs.
type mcpEnv struct {
	Box           string `json:"box"`
	Mode          string `json:"mode"` // "live" | "box"
	ContainerName string `json:"container_name"`
}

type provider struct{ pb.UnimplementedProviderServer }

// Invoke is the gRPC entry point for the ONE gRPC-served capability this plugin advertises:
// verb:mcp (the MCP check verb). command:mcp (`charly mcp …`) is NOT served over gRPC — it is
// dispatched by charly fork/exec'ing this binary in CLI mode (sdk.Main → cliMain, command.go),
// so it never reaches Invoke and is absent from Describe.
func (p provider) Invoke(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	return p.invokeVerb(ctx, req)
}

// invokeVerb runs one `mcp:` check operation. It decodes the full #Op, the typed
// plugin input (params.McpInput — the per-verb fields live in the desugared
// plugin_input since the schema-compaction cutover), and the env, skips in box
// mode (no live MCP server on a disposable `charly check box`), dispatches the
// method (metadata for `servers`, dial + MCP protocol otherwise), and
// self-evaluates the matchers.
func (p provider) invokeVerb(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	var op spec.Op
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &op); err != nil {
			return sdk.ResultJSON("fail", "mcp: decode op: "+err.Error())
		}
	}
	var in params.McpInput
	kit.DecodeInput(op.PluginInput, &in)
	var env mcpEnv
	if len(req.GetEnvJson()) > 0 {
		_ = json.Unmarshal(req.GetEnvJson(), &env)
	}
	method := in.Method

	// Live-deployment verb: skip under `charly check box` (no running MCP server on a
	// disposable `podman run --rm`) — mirrors the host's RunModeBox/box-mode skip.
	if env.Mode == "box" {
		return sdk.ResultJSON("skip", fmt.Sprintf("mcp: %s requires a running deployment (skip under charly check box)", method))
	}
	// No live deployment context → skip, the analogue of the host's empty-box skip.
	if env.Box == "" {
		return sdk.ResultJSON("skip", fmt.Sprintf("mcp: %s has no live deployment (box=%q)", method, env.Box))
	}
	// Resolve the MCP context via the GENERIC reverse-legs (cc.ResolveImageLabel for the declared
	// servers + cc.ResolveEndpoint for the host-routable URL) — the host owns the podman / OCI /
	// port-mapping machinery this out-of-process plugin cannot reach. Replaces the mcp preresolver.
	cc, err := sdk.NewCheckContext(req.GetExecutorBrokerId(), req.GetEnvJson())
	if err != nil {
		return sdk.ResultJSON("fail", fmt.Sprintf("mcp: %s: %v", method, err))
	}
	ep, err := resolveMcpEndpoint(ctx, cc, &env, method, in.McpName)
	if err != nil {
		return sdk.ResultJSON("fail", fmt.Sprintf("mcp: %s: %v", method, err))
	}

	out, runErr := dispatch(ctx, ep, &op, &in)

	// The shared exit/stdout/stderr verdict pipeline (R3). mcp produces no artifact.
	return sdk.VerbVerdict("mcp", method, out, runErr, &op, false)
}
