// Package harnesskind is the importable form of the charly HARNESS-SURFACE kinds: `skill`,
// `hook`, and `marketplace` — first-class entities carrying the marketplace skill corpus, the
// .claude/hooks/* gate scripts, and the marketplace/harness config into candy config (the
// plugins→candies migration). A KIND provider dispatches via the pb Invoke(OpLoad) envelope:
// decode the authored `skill:`/`hook:`/`marketplace:` entity from op.Params into the core
// spec.Skill/spec.Hook/spec.Marketplace type and re-marshal as canonical JSON; the host lands
// it in uf.PluginKinds["skill"]/["hook"]/["marketplace"][<name>] (the FLAT opaque-body path —
// these kinds are Structural:false: they nest no deploy resource members).
//
// The values are SELF-CONTAINED (scalars + inline block-scalar content — nothing rich or
// core-referencing like #Candy/#Vm), so — unlike candy/substrate — they ride op.Params and are
// validated against this plugin's served self-contained #SkillInput/#HookInput/#MarketplaceInput
// schema (validateAuthoredPluginInput, the flat-kind load gate). Usable COMPILED-IN
// (NewProvider()/NewMeta() via plugins_generated.go — the plugin-candy-kind placement, because
// the words must be recognized on every parse without an out-of-process build) OR served
// OUT-OF-PROCESS by the cmd/serve shim.
package harnesskind

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/spec/proto"
	"github.com/opencharly/spec/spec"
)

//go:embed schema/*.cue
var schemaFS embed.FS

const calver = "2026.218.1200"

// NewProvider returns the harness-kind provider for in-proc registration or out-of-proc serving.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta ships the three flat kind capabilities + their served self-contained schemas
// (fixedMeta.Describe compiles the embedded schemaFS's "schema" dir standalone).
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta(calver,
		[]sdk.ProvidedCapability{
			{Class: "kind", Word: "skill", InputDef: "#SkillInput"},
			{Class: "kind", Word: "hook", InputDef: "#HookInput"},
			{Class: "kind", Word: "marketplace", InputDef: "#MarketplaceInput"},
		},
		schemaFS)
}

type provider struct{ pb.UnimplementedProviderServer }

// Invoke handles OpLoad: decode the authored entity into its core spec type and return it
// re-marshalled as canonical JSON (the host validated it against the served #*Input schema
// first, via validateAuthoredPluginInput — the load gate).
func (provider) Invoke(_ context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	if req.GetOp() != sdk.OpLoad {
		return nil, fmt.Errorf("harness kind: unsupported op %q (only %q)", req.GetOp(), sdk.OpLoad)
	}
	if len(req.GetParamsJson()) == 0 {
		return nil, errors.New("harness kind: load requires a CUE input payload")
	}
	var out any
	switch req.GetReserved() {
	case "skill":
		out = &spec.Skill{}
	case "hook":
		out = &spec.Hook{}
	case "marketplace":
		out = &spec.Marketplace{}
	default:
		return nil, fmt.Errorf("harness kind: unsupported word %q", req.GetReserved())
	}
	if err := json.Unmarshal(req.GetParamsJson(), out); err != nil {
		return nil, fmt.Errorf("harness kind %q: decode entity: %w", req.GetReserved(), err)
	}
	res, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("harness kind %q: marshal entity: %w", req.GetReserved(), err)
	}
	return &pb.InvokeReply{ResultJson: res}, nil
}
