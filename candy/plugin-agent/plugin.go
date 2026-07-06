// Package agentkind is the importable form of charly's `agent` plugin KIND. A KIND provider
// dispatches via the pb Invoke(OpLoad) envelope — decode the authored `agent:` entity into
// the core spec.Agent and re-marshal as canonical JSON; the host lands it in
// uf.PluginKinds["agent"][<name>]. Usable COMPILED-IN (NewProvider()/NewMeta() via
// plugins_generated.go) OR served OUT-OF-PROCESS by the cmd/serve shim. Relocated out of
// charly's module (formerly charly/plugin/builtins/agent + charly/plugin_agent.go).
package agentkind

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk/spec"
)

//go:embed schema/*.cue
var schemaFS embed.FS

// NewProvider returns the kind provider for in-proc registration or out-of-proc serving.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta advertises the kind's capability (Class "kind", word "agent") + its
// self-contained CUE schema (via sdk.NewMeta → BuildCapabilities).
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta("2026.176.3201",
		[]sdk.ProvidedCapability{{Class: "kind", Word: "agent", InputDef: "#AgentInput"}},
		schemaFS)
}

type provider struct{ pb.UnimplementedProviderServer }

// Invoke handles two ops:
//   - OpLoad: decode the authored `agent:` entity into spec.Agent and return it
//     re-marshalled as canonical JSON (the host validated it against #AgentInput).
//   - OpResolve: the agent de-type (Cutover E) — the host hands the opaque agent
//     catalog + a selected name; this plugin applies name-selection + defaults and
//     returns a generic AgentExecSpec the kernel's harness runs (resolve.go).
func (provider) Invoke(_ context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	switch req.GetOp() {
	case sdk.OpLoad:
		var in spec.Agent
		if len(req.GetParamsJson()) > 0 {
			if err := json.Unmarshal(req.GetParamsJson(), &in); err != nil {
				return nil, fmt.Errorf("agent kind: decode entity: %w", err)
			}
		}
		out, err := json.Marshal(in)
		if err != nil {
			return nil, fmt.Errorf("agent kind: marshal entity: %w", err)
		}
		return &pb.InvokeReply{ResultJson: out}, nil
	case sdk.OpResolve:
		var in spec.AgentResolveInput
		if len(req.GetParamsJson()) > 0 {
			if err := json.Unmarshal(req.GetParamsJson(), &in); err != nil {
				return nil, fmt.Errorf("agent resolve: decode input: %w", err)
			}
		}
		reply, err := resolveAgent(in)
		if err != nil {
			return nil, err
		}
		out, err := json.Marshal(reply)
		if err != nil {
			return nil, fmt.Errorf("agent resolve: marshal reply: %w", err)
		}
		return &pb.InvokeReply{ResultJson: out}, nil
	default:
		return nil, fmt.Errorf("agent kind: unsupported op %q", req.GetOp())
	}
}
