// Package initkind is the importable form of charly's `init` plugin KIND: the
// init-system vocabulary (supervisord/systemd fragment-assembly + entrypoint +
// service-management templates). A KIND provider dispatches via the pb Invoke(OpLoad)
// envelope — the kind-class analogue of a verb's runPluginVerb — decoding the authored
// `init:` entity into the core spec.Init and re-marshalling it as canonical JSON; the
// host lands it in uf.PluginKinds["init"][<name>]. Usable in BOTH placements: COMPILED
// INTO charly (NewProvider()/NewMeta() via plugins_generated.go) OR served OUT-OF-PROCESS
// by the cmd/serve shim. Relocated out of charly's module (formerly
// charly/plugin/builtins/init + charly/plugin_init.go). Package initkind, not init —
// `init` is a reserved Go identifier; the directory + kind keyword stay `init`.
package initkind

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

// Invoke handles OpLoad: decode the authored (nameless) `init:` entity body into the core
// spec.Init and return it re-marshalled as canonical JSON (the host validated the body
// against #InitInput first; re-marshalling through spec.Init canonicalises it so
// UnifiedFile.Inits() reads uf.PluginKinds["init"] back into InitDef = spec.Init).
func (provider) Invoke(_ context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	if req.GetOp() != sdk.OpLoad {
		return nil, fmt.Errorf("init kind: unsupported op %q (only %q)", req.GetOp(), sdk.OpLoad)
	}
	var in spec.Init
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &in); err != nil {
			return nil, fmt.Errorf("init kind: decode entity: %w", err)
		}
	}
	out, err := json.Marshal(in)
	if err != nil {
		return nil, fmt.Errorf("init kind: marshal entity: %w", err)
	}
	return &pb.InvokeReply{ResultJson: out}, nil
}

type meta struct {
	pb.UnimplementedPluginMetaServer
}

// Describe ships the kind's capability (Class "kind", word "init") + its self-contained
// CUE schema via sdk.BuildCapabilities.
func (meta) Describe(context.Context, *pb.Empty) (*pb.Capabilities, error) {
	return sdk.BuildCapabilities("2026.176.3100",
		[]sdk.ProvidedCapability{{Class: "kind", Word: "init", InputDef: "#InitInput"}},
		schemaFS, "schema")
}
