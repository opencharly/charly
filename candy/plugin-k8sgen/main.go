// Package k8sgen — the OpEmit Invoke entrypoint. The host's in-core
// GenerateK8sKustomize shim resolves verb:k8sgen and Invokes OpEmit with a
// spec.K8sGenInput; this provider runs the pure generator (GenerateTree) and
// returns a spec.K8sGenReply of RELATIVE-pathed manifest docs. The host owns the
// disk I/O + the egress gate (see k8sgen.go for the carve-out rationale).
package k8sgen

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk/spec"
)

const calver = "2026.181.0001"

// NewProvider builds the k8sgen provider.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta advertises verb:k8sgen serving OpEmit (via sdk.NewMeta → BuildCapabilities). The verb is
// invoked with the structured spec.K8sGenInput, not an authored plugin_input, so it declares no
// #*Input — the shipped schema ships only the trivial #K8sgenInput so the host's plugin-schema gate
// has a non-empty, base-spliceable schema.
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta(calver,
		[]sdk.ProvidedCapability{{Class: "verb", Word: "k8sgen"}},
		nil)
}

type provider struct {
	pb.UnimplementedProviderServer
}

// Invoke handles OpEmit: decode the spec.K8sGenInput, run the pure generator, and
// return the spec.K8sGenReply (relative-pathed manifest docs) as JSON.
func (p *provider) Invoke(_ context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	if req.GetOp() != sdk.OpEmit {
		return nil, fmt.Errorf("k8sgen: unsupported op %q (only %q)", req.GetOp(), sdk.OpEmit)
	}
	var in spec.K8sGenInput
	if err := json.Unmarshal(req.GetParamsJson(), &in); err != nil {
		return nil, fmt.Errorf("k8sgen: decode input: %w", err)
	}
	reply, err := GenerateTree(in)
	if err != nil {
		return nil, err
	}
	out, err := json.Marshal(reply)
	if err != nil {
		return nil, err
	}
	return &pb.InvokeReply{ResultJson: out}, nil
}
