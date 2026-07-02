// Package tunnelverb is the importable form of the OUT-OF-TREE charly plugin serving
// the `tunnel` VERB (verb:tunnel) — the externalized tailscale/cloudflare TUNNEL
// EXECUTION LEG. It is usable in BOTH placements with zero authoring change: compiled
// INTO charly in-process (charly imports this package and registers
// NewProvider()/NewMeta() via the generated plugins_generated.go — it is listed in
// charly.yml `compiled_plugins:`) OR served OUT-OF-PROCESS by the cmd/serve shim through
// sdk.Serve. One provider, two placements; the schema travels with the plugin over
// Describe either way.
//
// The RESOLUTION half of the tunnel subsystem STAYS in charly's core
// (charly/tunnel.go: ResolveTunnelConfig / TunnelConfigFromMetadata + the pure
// schemeTarget/tailscaleFlag/isTCPFamily helpers the quadlet emitter shares). Only the
// EXECUTION leg lives HERE: charly's core tunnel_plugin.go resolves a TunnelConfig, then
// forwards TunnelStart / TunnelStop / cloudflareTunnelSetup over this verb's Invoke
// envelope ({method, config}); tunnel_exec.go runs the actual tailscale serve/funnel and
// cloudflared lifecycle, stopping at the exec/auth boundary.
package tunnelverb

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"

	"github.com/opencharly/charly/candy/plugin-tunnel/params"
	pb "github.com/opencharly/charly/charly/plugin/proto"
	"github.com/opencharly/charly/charly/plugin/sdk"
)

//go:embed schema/*.cue
var schemaFS embed.FS

// NewProvider returns the verb provider for in-proc registration or out-of-proc serving.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta returns the capability/schema describer.
func NewMeta() pb.PluginMetaServer { return &meta{} }

type provider struct{ pb.UnimplementedProviderServer }

// tunnelReply is the wire form the exec methods (start/stop/setup) return — byte-compatible
// with the core's tunnelReply (charly/tunnel_plugin.go), the cross-module tunnel contract.
type tunnelReply struct {
	Error      string `json:"error,omitempty"`
	Name       string `json:"name,omitempty"`
	ConfigPath string `json:"config_path,omitempty"`
}

// pluginCheckResult is the wire form the `plan` dry-run method returns — byte-compatible
// with the core's pluginCheckResult (charly/provider_checkenv.go) so a `plugin: tunnel`
// check step decodes it via the standard verb dispatch (status/message, no matcher pass).
type pluginCheckResult struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

// Invoke is the gRPC entry point for verb:tunnel. Both callers wrap the operation in a
// `plugin_input` envelope: the core adapter (tunnel_plugin.go) marshals {plugin_input:
// {method, config}} directly, and a `plugin: tunnel` CHECK step arrives as the marshaled
// Op which carries the authored `plugin_input` — so ONE decode path serves both.
func (provider) Invoke(_ context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	var in struct {
		PluginInput params.TunnelInput `json:"plugin_input"`
	}
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &in); err != nil {
			return replyJSON(tunnelReply{Error: fmt.Sprintf("plugin-tunnel: decode plugin_input: %v", err)})
		}
	}
	return dispatch(in.PluginInput)
}

// dispatch routes one tunnel operation. start/stop/setup EXECUTE (returning a tunnelReply
// the core adapter decodes); plan is the creds-free dry-run (returning a pluginCheckResult
// the check runner decodes). An error is captured on the reply (never panics) so the host
// always decodes a reply.
func dispatch(in params.TunnelInput) (*pb.InvokeReply, error) {
	errStr := func(err error) string {
		if err != nil {
			return err.Error()
		}
		return ""
	}
	switch in.Method {
	case "start":
		return replyJSON(tunnelReply{Error: errStr(tunnelStart(in.Config))})
	case "stop":
		return replyJSON(tunnelReply{Error: errStr(tunnelStop(in.Config))})
	case "setup":
		name, cfgPath, err := cloudflareTunnelSetup(in.Config)
		return replyJSON(tunnelReply{Name: name, ConfigPath: cfgPath, Error: errStr(err)})
	case "plan":
		return replyJSON(tunnelPlan(in.Config, in.Expect))
	default:
		return replyJSON(tunnelReply{Error: fmt.Sprintf("plugin-tunnel: unknown tunnel method %q", in.Method)})
	}
}

// replyJSON marshals any reply value into the InvokeReply envelope the host decodes.
func replyJSON(v any) (*pb.InvokeReply, error) {
	j, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return &pb.InvokeReply{ResultJson: j}, nil
}

type meta struct {
	pb.UnimplementedPluginMetaServer
}

// Describe ships verb:tunnel + its self-contained CUE schema via sdk.BuildCapabilities.
// Unlike most externalized verbs, verb:tunnel DOES carry an authored plugin_input (the
// `plan` dry-run step's {method,config,expect}), so it advertises the #TunnelInput def;
// the SDK compiles the schema standalone here, failing loudly before serving if broken.
func (meta) Describe(context.Context, *pb.Empty) (*pb.Capabilities, error) {
	return sdk.BuildCapabilities("2026.182.1200",
		[]sdk.ProvidedCapability{{Class: "verb", Word: "tunnel", InputDef: "#TunnelInput"}},
		schemaFS, "schema")
}
