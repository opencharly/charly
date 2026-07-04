// Package exampleprobe is the importable form of charly's reference plugin candy:
// it serves the `exampleprobe` check verb (a deterministic pass-with-marker probe)
// + its self-contained CUE schema, usable in BOTH placements with zero authoring
// change — COMPILED INTO charly in-process (charly imports this package and
// registers NewProvider()/NewMeta() via plugins_generated.go) OR served
// OUT-OF-PROCESS over go-plugin gRPC by the cmd/serve shim. The canonical
// "a plugin is a candy" example, now with its Go relocated OUT of charly's module
// into the candy itself (formerly charly/plugin/builtins/exampleprobe +
// charly/plugin_example.go).
package exampleprobe

import (
	"context"
	"embed"
	"encoding/json"

	"github.com/opencharly/charly/candy/plugin-example/params"
	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
)

//go:embed schema/*.cue
var schemaFS embed.FS

// NewProvider returns the verb provider for in-proc registration or out-of-proc serving.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta ships the plugin's capabilities + its self-contained CUE schema via
// sdk.NewMeta → BuildCapabilities (compiled standalone, failing loudly if broken/empty).
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta("2026.176.1200",
		[]sdk.ProvidedCapability{{Class: "verb", Word: "exampleprobe", InputDef: "#ExampleprobeInput"}},
		schemaFS)
}

type provider struct{ pb.UnimplementedProviderServer }

// Invoke handles the `exampleprobe` verb: a deterministic pass echoing
// plugin_input.marker (so a bed asserts the value round-trips author -> provider ->
// result). It decodes into the CUE-GENERATED params.ExampleprobeInput, never a
// hand-parsed map.
func (provider) Invoke(_ context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	var in struct {
		PluginInput params.ExampleprobeInput `json:"plugin_input"`
	}
	if len(req.GetParamsJson()) > 0 {
		_ = json.Unmarshal(req.GetParamsJson(), &in)
	}
	marker := in.PluginInput.Marker
	if marker == "" {
		marker = "exampleprobe-ok"
	}
	j, err := json.Marshal(map[string]string{"status": "pass", "message": marker})
	if err != nil {
		return nil, err
	}
	return &pb.InvokeReply{ResultJson: j}, nil
}
