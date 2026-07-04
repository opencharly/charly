// Package sidecarkind is the importable form of charly's `sidecar` plugin KIND. A KIND provider
// dispatches via the pb Invoke(OpLoad) envelope — decode the authored `sidecar:` entity into
// the core spec.Sidecar and re-marshal as canonical JSON; the host lands it in
// uf.PluginKinds["sidecar"][<name>]. Usable COMPILED-IN (NewProvider()/NewMeta() via
// plugins_generated.go) OR served OUT-OF-PROCESS by the cmd/serve shim. Relocated out of
// charly's module (formerly charly/plugin/builtins/sidecar + charly/plugin_sidecar.go).
package sidecarkind

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

// NewMeta advertises kind:sidecar + its self-contained CUE schema (via sdk.NewMeta →
// BuildCapabilities).
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta("2026.176.3207",
		[]sdk.ProvidedCapability{{Class: "kind", Word: "sidecar", InputDef: "#SidecarInput"}},
		schemaFS)
}

type provider struct{ pb.UnimplementedProviderServer }

// Invoke handles OpLoad: decode the authored `sidecar:` entity into spec.Sidecar and return it
// re-marshalled as canonical JSON (the host validated the body against #SidecarInput first).
func (provider) Invoke(_ context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	if req.GetOp() != sdk.OpLoad {
		return nil, fmt.Errorf("sidecar kind: unsupported op %q (only %q)", req.GetOp(), sdk.OpLoad)
	}
	var in spec.Sidecar
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &in); err != nil {
			return nil, fmt.Errorf("sidecar kind: decode entity: %w", err)
		}
	}
	out, err := json.Marshal(in)
	if err != nil {
		return nil, fmt.Errorf("sidecar kind: marshal entity: %w", err)
	}
	return &pb.InvokeReply{ResultJson: out}, nil
}
