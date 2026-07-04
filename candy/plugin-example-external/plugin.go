// Package externalprobe is the importable form of the reference charly plugin: it
// serves the `externalprobe` check verb (pb.ProviderServer) and its self-contained
// CUE schema (pb.PluginMetaServer.Describe), usable in BOTH placements with zero
// authoring change — compiled INTO charly in-process (charly imports this package
// and registers NewProvider()/NewMeta() via the generated plugins_generated.go) OR
// served OUT-OF-PROCESS by the cmd/serve shim through sdk.Serve. One provider, two
// placements: the schema travels with the plugin over Describe either way, so the
// host validates authored plugin_input against base ++ this schema identically.
package externalprobe

import (
	"context"
	"embed"
	"encoding/json"

	"github.com/opencharly/charly/candy/plugin-example-external/params"
	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk"
)

//go:embed schema/*.cue
var schemaFS embed.FS

// NewProvider returns the verb provider for in-proc registration or out-of-proc serving.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta returns the capability/schema describer.
func NewMeta() pb.PluginMetaServer { return &meta{} }

type provider struct{ pb.UnimplementedProviderServer }

// Invoke handles the `externalprobe` verb: returns a deterministic pass, echoing
// plugin_input.marker so a bed can assert the value travelled author -> wire ->
// provider -> result (the params round-trip). It decodes into the CUE-GENERATED
// typed struct (params.ExternalprobeInput), never a hand-parsed map.
func (provider) Invoke(_ context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	var in struct {
		PluginInput params.ExternalprobeInput `json:"plugin_input"`
	}
	if len(req.GetParamsJson()) > 0 {
		_ = json.Unmarshal(req.GetParamsJson(), &in)
	}
	marker := in.PluginInput.Marker
	if marker == "" {
		marker = "externalprobe-ok"
	}
	j, err := json.Marshal(map[string]string{"status": "pass", "message": marker})
	if err != nil {
		return nil, err
	}
	return &pb.InvokeReply{ResultJson: j}, nil
}

type meta struct {
	pb.UnimplementedPluginMetaServer
}

// Describe ships the plugin's capabilities AND its self-contained CUE schema via
// sdk.BuildCapabilities — compiled standalone here, failing loudly if broken/empty.
func (meta) Describe(context.Context, *pb.Empty) (*pb.Capabilities, error) {
	return sdk.BuildCapabilities("2026.172.0001",
		[]sdk.ProvidedCapability{{Class: "verb", Word: "externalprobe", InputDef: "#ExternalprobeInput"}},
		schemaFS, "schema")
}
