// Package matching is the importable form of charly's `matching` check verb: pure
// in-process value matching (no target probe) — it coerces plugin_input.matching to a
// string and asserts every plugin_input.contains goss-style matcher against it. A
// STATELESS provider (no live *Runner needed), so it serves itself over the pb Invoke
// envelope in BOTH placements with zero authoring change — COMPILED INTO charly
// in-process (NewProvider()/NewMeta() via plugins_generated.go) OR served
// OUT-OF-PROCESS over go-plugin gRPC by the cmd/serve shim. Relocated out of charly's
// module (formerly charly/plugin/builtins/matching + charly/plugin_matching.go); the
// matcher evaluation reuses the SHARED sdk helpers (sdk.MatchAll / sdk.MatchValueString
// — the single matcher implementation, R3).
package matching

import (
	"context"
	"embed"
	"encoding/json"

	"github.com/opencharly/charly/candy/plugin-matching/params"
	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk/spec"
)

//go:embed schema/*.cue
var schemaFS embed.FS

// NewProvider returns the verb provider for in-proc registration or out-of-proc serving.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta advertises verb:matching + the plugin's self-contained CUE schema (via
// sdk.NewMeta → BuildCapabilities), compiled standalone and failing loudly if broken/empty.
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta("2026.176.2100",
		[]sdk.ProvidedCapability{{Class: "verb", Word: "matching", InputDef: "#MatchingInput", Primary: "matching"}},
		schemaFS)
}

type provider struct{ pb.UnimplementedProviderServer }

// Invoke coerces plugin_input.matching to a string and asserts every
// plugin_input.contains matcher against it. It decodes into the CUE-GENERATED
// params.MatchingInput; gengotypes degrades the self-contained matcher disjunction to
// `any`, so Contains is re-decoded through the SHARED matcher codec
// (spec.MatcherList's UnmarshalJSON — R3) into the typed []sdk.Matcher MatchAll consumes.
func (provider) Invoke(_ context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	var in struct {
		PluginInput params.MatchingInput `json:"plugin_input"`
	}
	if len(req.GetParamsJson()) > 0 {
		_ = json.Unmarshal(req.GetParamsJson(), &in)
	}
	value := sdk.MatchValueString(in.PluginInput.Matching)
	var contains spec.MatcherList
	if in.PluginInput.Contains != nil {
		raw, err := json.Marshal(in.PluginInput.Contains)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &contains); err != nil {
			return nil, err
		}
	}
	status, message := "pass", "value="+value
	if err := sdk.MatchAll(value, contains); err != nil {
		status, message = "fail", err.Error()
	}
	j, err := json.Marshal(map[string]string{"status": status, "message": message})
	if err != nil {
		return nil, err
	}
	return &pb.InvokeReply{ResultJson: j}, nil
}
