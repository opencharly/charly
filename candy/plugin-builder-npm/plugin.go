// Package buildernpm is the charly plugin serving the `npm` builder's
// build-time multi-stage AND its deploy-time IR shim. Its BUILD-TIME multi-stage — the
// `FROM <builder> AS …` block + COPY artifacts — is resolved HERE via OpResolve →
// kit.BuilderResolve (C10, no longer the core embedded builder: vocabulary); its deploy-time legs:
//
//   - OpCollectContext → the per-candy stage-context keys the host records on a BuilderStep
//     (npm records none — globals are read from package.json host-side at install time); and
//   - OpReverse → that step's teardown ops (npm → npm-uninstall-g when globals are known).
//
// The host invokes both in its build PRE-PASS (BEFORE the pure BuildDeployPlan compile), keeping the
// compiler pure. The per-builder LOGIC is the shared sdk/kit (R3); this package is only the
// composable selection point. Dual-placement by construction: the SAME NewProvider()/NewMeta()
// compile INTO charly in-process when listed in compiled_plugins, or cmd/serve serves them
// OUT-OF-PROCESS over go-plugin gRPC when they are not — placement is invisible above the registry.
package buildernpm

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/kit"
	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk/spec"
)

//go:embed schema/*.cue
var schemaFS embed.FS

// builderWord is the reserved builder word this plugin serves.
const builderWord = "npm"

// NewProvider returns the buildernpm provider.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta advertises the builder:npm capability + the plugin's self-contained CUE schema
// (via sdk.NewMeta → BuildCapabilities), compiled standalone and failing loudly if broken.
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta("2026.182.0200",
		[]sdk.ProvidedCapability{{Class: "builder", Word: builderWord, InputDef: "#NpmBuilderInput"}},
		schemaFS)
}

type provider struct{ pb.UnimplementedProviderServer }

// Invoke dispatches the build-time OpResolve (→ kit.BuilderResolve: the multi-stage Stage +
// CopyArtifacts / InlineFragment) and the two deploy-time IR ops (OpCollectContext / OpReverse) to
// the shared kit logic. Any other op is a loud error.
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
