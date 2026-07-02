// Package examplebootstrap is the importable reference BOOTSTRAP-PHASE plugin (F9): a compiled-in
// plugin declaring Phase=="bootstrap", so the kernel invokes its OpBootstrap on the RAW project
// config bytes BEFORE config validation/migration (runBootstrapPhase, called in LoadUnified before
// the schema gate). This no-op returns the bytes UNCHANGED — it proves the bootstrap hook fires at
// the right time without mutating anything; a real bootstrap-phase plugin would transform a
// stale config's bytes here. Bootstrap plugins are COMPILED-IN only (no validated config exists yet
// to discover an out-of-process source), so this connects in-proc with no LoadUnified re-entry. The
// bootstrap-phase analogue of the verb-class candy/plugin-example-external.
package examplebootstrap

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"

	pb "github.com/opencharly/charly/charly/plugin/proto"
	"github.com/opencharly/charly/charly/plugin/sdk"
)

//go:embed schema/*.cue
var schemaFS embed.FS

const calver = "2026.181.0001"

// NewProvider returns the bootstrap provider for in-proc (compiled-in) registration or out-of-proc serving.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta returns the capability/schema describer.
func NewMeta() pb.PluginMetaServer { return &meta{} }

type provider struct{ pb.UnimplementedProviderServer }

// Invoke handles OpBootstrap: the kernel passes the raw project config bytes ({"config": …}); this
// NO-OP returns them UNCHANGED ({"config": <same>}). A real bootstrap plugin would return
// transformed bytes the kernel applies before the schema gate.
func (provider) Invoke(_ context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	if req.GetOp() != sdk.OpBootstrap {
		return nil, fmt.Errorf("examplebootstrap: unsupported op %q (only %q)", req.GetOp(), sdk.OpBootstrap)
	}
	var in struct {
		Config string `json:"config"`
	}
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &in); err != nil {
			return nil, fmt.Errorf("examplebootstrap: decode config: %w", err)
		}
	}
	out, err := json.Marshal(map[string]string{"config": in.Config})
	if err != nil {
		return nil, fmt.Errorf("examplebootstrap: marshal reply: %w", err)
	}
	return &pb.InvokeReply{ResultJson: out}, nil
}

type meta struct {
	pb.UnimplementedPluginMetaServer
}

// Describe advertises the capability with Phase "bootstrap" (F9) — the host enumerates it in the
// bootstrap phase (providersInPhase) and invokes OpBootstrap before config validation. No InputDef:
// a bootstrap plugin is invoked with the raw config, not a structured plugin_input.
func (meta) Describe(context.Context, *pb.Empty) (*pb.Capabilities, error) {
	return sdk.BuildCapabilities(calver,
		[]sdk.ProvidedCapability{{Class: "verb", Word: "examplebootstrap", Phase: sdk.PhaseBootstrap}},
		schemaFS, "schema")
}
