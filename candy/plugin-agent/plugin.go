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

	pb "github.com/opencharly/charly/charly/plugin/proto"
	"github.com/opencharly/charly/charly/plugin/sdk"
	"github.com/opencharly/charly/charly/spec"
)

//go:embed schema/*.cue
var schemaFS embed.FS

// NewProvider returns the kind provider for in-proc registration or out-of-proc serving.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta returns the capability/schema describer.
func NewMeta() pb.PluginMetaServer { return &meta{} }

type provider struct{ pb.UnimplementedProviderServer }

// Invoke handles OpLoad: decode the authored `agent:` entity into spec.Agent and return it
// re-marshalled as canonical JSON (the host validated the body against #AgentInput first).
func (provider) Invoke(_ context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	if req.GetOp() != sdk.OpLoad {
		return nil, fmt.Errorf("agent kind: unsupported op %q (only %q)", req.GetOp(), sdk.OpLoad)
	}
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
}

type meta struct {
	pb.UnimplementedPluginMetaServer
}

// Describe ships the kind's capability (Class "kind", word "agent") + its self-contained CUE schema.
func (meta) Describe(context.Context, *pb.Empty) (*pb.Capabilities, error) {
	return sdk.BuildCapabilities("2026.176.3201",
		[]sdk.ProvidedCapability{{Class: "kind", Word: "agent", InputDef: "#AgentInput"}},
		schemaFS, "schema")
}
