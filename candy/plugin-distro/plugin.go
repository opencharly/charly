// Package distrokind is the importable form of charly's `distro` plugin KIND. A KIND provider
// dispatches via the pb Invoke(OpLoad) envelope — decode the authored `distro:` entity into
// the core spec.Distro and re-marshal as canonical JSON; the host lands it in
// uf.PluginKinds["distro"][<name>]. Usable COMPILED-IN (NewProvider()/NewMeta() via
// plugins_generated.go) OR served OUT-OF-PROCESS by the cmd/serve shim. Relocated out of
// charly's module (formerly charly/plugin/builtins/distro + charly/plugin_distro.go).
package distrokind

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

// NewMeta ships the distro kind's capability (Class "kind", word "distro") + its
// self-contained CUE schema, via sdk.NewMeta → BuildCapabilities.
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta("2026.176.3203",
		[]sdk.ProvidedCapability{{Class: "kind", Word: "distro", InputDef: "#DistroInput"}},
		schemaFS)
}

type provider struct{ pb.UnimplementedProviderServer }

// Invoke handles OpLoad: decode the authored `distro:` entity into spec.Distro and return it
// re-marshalled as canonical JSON (the host validated the body against #DistroInput first).
func (provider) Invoke(_ context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	switch req.GetOp() {
	case sdk.OpLoad:
		var in spec.Distro
		if len(req.GetParamsJson()) > 0 {
			if err := json.Unmarshal(req.GetParamsJson(), &in); err != nil {
				return nil, fmt.Errorf("distro kind: decode entity: %w", err)
			}
		}
		out, err := json.Marshal(in)
		if err != nil {
			return nil, fmt.Errorf("distro kind: marshal entity: %w", err)
		}
		return &pb.InvokeReply{ResultJson: out}, nil
	case sdk.OpResolve:
		// The distro de-type (Cutover M): project the opaque distro body into a
		// ResolvedDistro the kernel's build engine consumes.
		var in spec.DistroResolveInput
		if len(req.GetParamsJson()) > 0 {
			if err := json.Unmarshal(req.GetParamsJson(), &in); err != nil {
				return nil, fmt.Errorf("distro resolve: decode input: %w", err)
			}
		}
		reply, err := resolveDistro(in)
		if err != nil {
			return nil, err
		}
		out, err := json.Marshal(reply)
		if err != nil {
			return nil, fmt.Errorf("distro resolve: marshal reply: %w", err)
		}
		return &pb.InvokeReply{ResultJson: out}, nil
	default:
		return nil, fmt.Errorf("distro kind: unsupported op %q", req.GetOp())
	}
}
