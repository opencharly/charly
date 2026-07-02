// Command plugin-example-kind is a reference OUT-OF-TREE charly class:kind plugin (F4): it
// serves the `examplekind` entity KIND over go-plugin gRPC. It proves PARSE-TIME kind prescan +
// pre-load connect — a `kind: examplekind` entity whose plugin is NOT compiled in is RECOGNIZED
// at parse, the plugin is CONNECTED before decode, and runPluginKind dispatches Invoke(OpLoad)
// so the authored body lands in uf.PluginKinds["examplekind"][<name>]. The kind-class companion
// of the verb/deploy/step example plugins. NOT listed in compiled_plugins — OUT-OF-PROCESS only,
// so it is the witness that the loader can recognize + connect a kind plugin it was not built with.
package main

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

const calver = "2026.181.0001"

func main() { sdk.Serve(&provider{}, &meta{}) }

type provider struct{ pb.UnimplementedProviderServer }

// examplekindBody is the authored `examplekind:` entity body the host assembles + validates
// against #ExamplekindInput before OpLoad.
type examplekindBody struct {
	Marker string `json:"marker"`
}

// Invoke handles OpLoad (decode the authored body into uf.PluginKinds) AND the F7/C8 OpValidate
// (a deep plugin-owned check returning spec.Diagnostics beyond the static CUE input-def gate). The
// flat external-kind decode channel works for a plugin CONNECTED AT PARSE by the F4 prescan.
func (provider) Invoke(_ context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	var in examplekindBody
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &in); err != nil {
			return nil, fmt.Errorf("examplekind: decode entity: %w", err)
		}
	}
	switch req.GetOp() {
	case sdk.OpLoad:
		out, err := json.Marshal(in)
		if err != nil {
			return nil, fmt.Errorf("examplekind: marshal entity: %w", err)
		}
		return &pb.InvokeReply{ResultJson: out}, nil
	case sdk.OpValidate:
		// The deep check: reject the sentinel marker "INVALID" with an error Diagnostic — proving
		// the host dispatches OpValidate + surfaces error-severity Diagnostics as a load failure.
		var diags spec.Diagnostics
		if in.Marker == "INVALID" {
			diags.Items = append(diags.Items, spec.Diagnostic{Severity: "error", Path: "marker", Message: `marker "INVALID" is rejected by examplekind's deep OpValidate check`})
		}
		out, err := json.Marshal(diags)
		if err != nil {
			return nil, fmt.Errorf("examplekind: marshal diagnostics: %w", err)
		}
		return &pb.InvokeReply{ResultJson: out}, nil
	default:
		return nil, fmt.Errorf("examplekind: unsupported op %q (only %q, %q)", req.GetOp(), sdk.OpLoad, sdk.OpValidate)
	}
}

type meta struct {
	pb.UnimplementedPluginMetaServer
}

// Describe ships the kind's capability (Class "kind", word "examplekind") + its self-contained
// CUE schema over the SAME Describe channel a compiled-in kind uses.
func (meta) Describe(context.Context, *pb.Empty) (*pb.Capabilities, error) {
	return sdk.BuildCapabilities(calver,
		[]sdk.ProvidedCapability{{Class: "kind", Word: "examplekind", InputDef: "#ExamplekindInput", Validates: true}},
		schemaFS, "schema")
}
