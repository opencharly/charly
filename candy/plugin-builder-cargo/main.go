// Command plugin-builder-cargo is the OUT-OF-TREE charly plugin serving the `cargo` builder's
// build-time multi-stage AND its deploy-time IR shim. Its BUILD-TIME multi-stage — the inline
// `RUN cargo install` step — is resolved HERE via OpResolve → kit.BuilderResolve (C10, no longer
// the core embedded builder: vocabulary); its deploy-time legs:
//
//   - OpCollectContext → the per-candy stage-context keys the host records on a BuilderStep
//     (cargo records none — binaries are read from Cargo.toml [[bin]] host-side at install time); and
//   - OpReverse → that step's teardown ops (cargo → cargo-uninstall when binaries are known).
//
// The host invokes both in its build PRE-PASS (BEFORE the pure BuildDeployPlan compile), keeping the
// compiler pure. The per-builder LOGIC is the shared charly/plugin/kit (R3); this module is only the
// composable selection point + serve shim.
package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"

	"github.com/opencharly/charly/charly/plugin/kit"
	pb "github.com/opencharly/charly/charly/plugin/proto"
	"github.com/opencharly/charly/charly/plugin/sdk"
	"github.com/opencharly/charly/charly/spec"
)

//go:embed schema/*.cue
var schemaFS embed.FS

// builderWord is the reserved builder word this plugin serves.
const builderWord = "cargo"

func main() { sdk.Serve(&provider{}, &meta{}) }

type provider struct{ pb.UnimplementedProviderServer }

// Invoke dispatches the build-time OpResolve (→ kit.BuilderResolve: the inline builder's
// InlineFragment) and the two deploy-time IR ops (OpCollectContext / OpReverse) to the shared kit
// logic. Any other op is a loud error.
func (provider) Invoke(_ context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	switch req.GetOp() {
	case sdk.OpCollectContext:
		var in spec.BuilderCollectInput
		if len(req.GetParamsJson()) > 0 {
			if err := json.Unmarshal(req.GetParamsJson(), &in); err != nil {
				return nil, fmt.Errorf("builder %q: decode collect-context input: %w", builderWord, err)
			}
		}
		j, err := json.Marshal(spec.BuilderCollectReply{Context: kit.BuilderCollectContext(builderWord, in)})
		if err != nil {
			return nil, err
		}
		return &pb.InvokeReply{ResultJson: j}, nil
	case sdk.OpReverse:
		var in spec.BuilderReverseInput
		if len(req.GetParamsJson()) > 0 {
			if err := json.Unmarshal(req.GetParamsJson(), &in); err != nil {
				return nil, fmt.Errorf("builder %q: decode reverse input: %w", builderWord, err)
			}
		}
		j, err := json.Marshal(spec.BuilderReverseReply{ReverseOps: kit.BuilderReverse(builderWord, in)})
		if err != nil {
			return nil, err
		}
		return &pb.InvokeReply{ResultJson: j}, nil
	case sdk.OpResolve:
		var in spec.BuilderResolveInput
		if len(req.GetParamsJson()) > 0 {
			if err := json.Unmarshal(req.GetParamsJson(), &in); err != nil {
				return nil, fmt.Errorf("builder %q: decode resolve input: %w", builderWord, err)
			}
		}
		reply, err := kit.BuilderResolve(builderWord, in)
		if err != nil {
			return nil, err
		}
		j, err := json.Marshal(reply)
		if err != nil {
			return nil, err
		}
		return &pb.InvokeReply{ResultJson: j}, nil
	}
	return nil, fmt.Errorf("builder %q: unsupported op %q (serves only %q, %q, %q)", builderWord, req.GetOp(), sdk.OpResolve, sdk.OpCollectContext, sdk.OpReverse)
}

type meta struct {
	pb.UnimplementedPluginMetaServer
}

// Describe advertises the builder:cargo capability + its self-contained CUE schema over the same
// channel a builtin uses; BuildCapabilities compiles the schema standalone, failing loudly if broken.
func (meta) Describe(context.Context, *pb.Empty) (*pb.Capabilities, error) {
	return sdk.BuildCapabilities("2026.182.0300",
		[]sdk.ProvidedCapability{{Class: "builder", Word: builderWord, InputDef: "#CargoBuilderInput"}},
		schemaFS, "schema")
}
