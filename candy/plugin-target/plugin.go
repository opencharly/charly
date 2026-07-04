// Package targetkind is the importable form of charly's `target` plugin KIND. A KIND provider
// dispatches via the pb Invoke(OpLoad) envelope — decode the authored `target:` entity into
// the core spec.Target and re-marshal as canonical JSON; the host lands it in
// uf.PluginKinds["target"][<name>]. Usable COMPILED-IN (NewProvider()/NewMeta() via
// plugins_generated.go) OR served OUT-OF-PROCESS by the cmd/serve shim. Relocated out of
// charly's module (formerly charly/plugin/builtins/target + charly/plugin_target.go).
package targetkind

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

// NewMeta advertises kind:target + its self-contained CUE schema (via sdk.NewMeta →
// BuildCapabilities).
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta("2026.176.3208",
		[]sdk.ProvidedCapability{{Class: "kind", Word: "target", InputDef: "#TargetInput"}},
		schemaFS)
}

type provider struct{ pb.UnimplementedProviderServer }

// Invoke handles OpLoad: decode the authored `target:` entity into spec.Target and return it
// re-marshalled as canonical JSON (the host validated the body against #TargetInput first).
func (provider) Invoke(_ context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	if req.GetOp() != sdk.OpLoad {
		return nil, fmt.Errorf("target kind: unsupported op %q (only %q)", req.GetOp(), sdk.OpLoad)
	}
	var in spec.Target
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &in); err != nil {
			return nil, fmt.Errorf("target kind: decode entity: %w", err)
		}
	}
	out, err := json.Marshal(in)
	if err != nil {
		return nil, fmt.Errorf("target kind: marshal entity: %w", err)
	}
	return &pb.InvokeReply{ResultJson: out}, nil
}
