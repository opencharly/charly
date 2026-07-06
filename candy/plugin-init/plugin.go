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

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk/spec"
)

//go:embed schema/*.cue
var schemaFS embed.FS

// NewProvider returns the kind provider for in-proc registration or out-of-proc serving.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta ships the kind's capability (Class "kind", word "init") + its self-contained
// CUE schema via sdk.NewMeta → BuildCapabilities.
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta("2026.176.3100",
		[]sdk.ProvidedCapability{{Class: "kind", Word: "init", InputDef: "#InitInput"}},
		schemaFS)
}

type provider struct{ pb.UnimplementedProviderServer }

// Invoke handles OpLoad: decode the authored (nameless) `init:` entity body into the core
// spec.Init and return it re-marshalled as canonical JSON (the host validated the body
// against #InitInput first; re-marshalling through spec.Init canonicalises it so
// UnifiedFile.Inits() reads uf.PluginKinds["init"] back into InitDef = spec.Init).
func (provider) Invoke(_ context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	switch req.GetOp() {
	case sdk.OpLoad:
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
	case sdk.OpResolve:
		// The init de-type (Cutover F): Render (leg 1 — one service unit) or Config
		// (legs 2–4 — the resolved init envelope of build/label/entrypoint values).
		var reqIn spec.InitResolveRequest
		if len(req.GetParamsJson()) > 0 {
			if err := json.Unmarshal(req.GetParamsJson(), &reqIn); err != nil {
				return nil, fmt.Errorf("init resolve: decode input: %w", err)
			}
		}
		var (
			out []byte
			err error
		)
		switch {
		case reqIn.Render != nil:
			reply, rerr := renderServiceUnit(*reqIn.Render)
			if rerr != nil {
				return nil, rerr
			}
			out, err = json.Marshal(reply)
		case reqIn.Config != nil:
			reply, rerr := resolveInitConfig(*reqIn.Config)
			if rerr != nil {
				return nil, rerr
			}
			out, err = json.Marshal(reply)
		default:
			return nil, fmt.Errorf("init resolve: empty request (neither render nor config)")
		}
		if err != nil {
			return nil, fmt.Errorf("init resolve: marshal reply: %w", err)
		}
		return &pb.InvokeReply{ResultJson: out}, nil
	default:
		return nil, fmt.Errorf("init kind: unsupported op %q", req.GetOp())
	}
}
