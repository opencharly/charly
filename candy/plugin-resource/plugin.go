// Package resourcekind is the importable form of charly's `resource` plugin KIND. A KIND provider
// dispatches via the pb Invoke(OpLoad) envelope — decode the authored `resource:` entity into
// the core spec.Resource and re-marshal as canonical JSON; the host lands it in
// uf.PluginKinds["resource"][<name>]. Usable COMPILED-IN (NewProvider()/NewMeta() via
// plugins_generated.go) OR served OUT-OF-PROCESS by the cmd/serve shim. Relocated out of
// charly's module (formerly charly/plugin/builtins/resource + charly/plugin_resource.go).
package resourcekind

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

// NewMeta advertises kind:resource + the plugin's self-contained CUE schema (via sdk.NewMeta → BuildCapabilities).
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta("2026.176.3206",
		[]sdk.ProvidedCapability{{Class: "kind", Word: "resource", InputDef: "#ResourceInput"}},
		schemaFS)
}

type provider struct{ pb.UnimplementedProviderServer }

// Invoke handles OpLoad: decode the authored `resource:` entity into spec.Resource and return it
// re-marshalled as canonical JSON (the host validated the body against #ResourceInput first).
func (provider) Invoke(_ context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	switch req.GetOp() {
	case sdk.OpLoad:
		var in spec.Resource
		if len(req.GetParamsJson()) > 0 {
			if err := json.Unmarshal(req.GetParamsJson(), &in); err != nil {
				return nil, fmt.Errorf("resource kind: decode entity: %w", err)
			}
		}
		out, err := json.Marshal(in)
		if err != nil {
			return nil, fmt.Errorf("resource kind: marshal entity: %w", err)
		}
		return &pb.InvokeReply{ResultJson: out}, nil
	case sdk.OpResolve:
		// The resource de-type (Cutover G): project the opaque resource body into a
		// ResolvedResource the GPU arbiter consumes.
		var in spec.ResourceResolveInput
		if len(req.GetParamsJson()) > 0 {
			if err := json.Unmarshal(req.GetParamsJson(), &in); err != nil {
				return nil, fmt.Errorf("resource resolve: decode input: %w", err)
			}
		}
		reply, err := resolveResource(in)
		if err != nil {
			return nil, err
		}
		out, err := json.Marshal(reply)
		if err != nil {
			return nil, fmt.Errorf("resource resolve: marshal reply: %w", err)
		}
		return &pb.InvokeReply{ResultJson: out}, nil
	default:
		return nil, fmt.Errorf("resource kind: unsupported op %q", req.GetOp())
	}
}
