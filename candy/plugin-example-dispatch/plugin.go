// Package exampledispatch is the charly example plugin (an importable, dual-placement root package) proving the F10 reverse legs:
// during its own Invoke (run WITH a reverse channel), it calls BACK to the host to (1) INVOKE
// ANOTHER plugin's verb (sdk.Executor.InvokeProvider — plugin↔plugin via the host broker) and
// (2) request a HOST-BUILD (sdk.Executor.HostBuild — the host runs a registered host-builder),
// returning both results. This exercises the NESTED-BROKER round-trip (A's Invoke holds a broker;
// the host stands up a SECOND broker to dispatch the peer) generically — the generalization of the
// RunHostStep ExternalPlugin arm to any class/op + a standalone build request.
package exampledispatch

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
)

//go:embed schema/*.cue
var schemaFS embed.FS

const calver = "2026.181.0001"

// NewProvider returns the exampledispatch provider.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta returns the plugin's capability/schema describer.
func NewMeta() pb.PluginMetaServer { return &meta{} }

type provider struct{ pb.UnimplementedProviderServer }

// dispatchInput is the op's plugin_input: which peer verb to invoke + which candy binary to host-build.
type dispatchInput struct {
	TargetWord    string `json:"target_word"`     // a verb word the host resolves + Invokes (plugin↔plugin)
	BuildCandyDir string `json:"build_candy_dir"` // a candy dir the host builds a plugin binary for (host-build)
	BuildName     string `json:"build_name"`
}

// Invoke dispatches by word: the PEER verb (exampledispatchpeer) is a trivial echo target other
// plugins invoke via InvokeProvider (the OUT-OF-PROCESS target proving the nested-broker round-trip);
// the dispatcher verb (exampledispatch) exercises both F10 legs.
func (provider) Invoke(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	if req.GetReserved() == "exampledispatchpeer" {
		// The OOP peer: echo a deterministic marker so a caller can assert it was reached over the
		// nested broker. (It does NOT call back — it is the leaf of the plugin→host→plugin chain.)
		out, _ := json.Marshal(map[string]string{"status": "pass", "message": "peer-reached"})
		return &pb.InvokeReply{ResultJson: out}, nil
	}
	if req.GetOp() != sdk.OpRun {
		return nil, fmt.Errorf("exampledispatch: unsupported op %q (only %q)", req.GetOp(), sdk.OpRun)
	}
	exec, err := sdk.ExecutorFromInvoke(req.GetExecutorBrokerId())
	if err != nil {
		return nil, fmt.Errorf("exampledispatch: no host executor: %w", err)
	}
	var in dispatchInput
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &in); err != nil {
			return nil, fmt.Errorf("exampledispatch: decode input: %w", err)
		}
	}
	out := map[string]json.RawMessage{}

	// (1) plugin↔plugin: invoke the target verb on the host's behalf (the host resolves it +
	// dispatches over a nested broker). A valid #*Input for the reference verb is {"marker": …}.
	if in.TargetWord != "" {
		pres, err := exec.InvokeProvider(ctx, "verb", in.TargetWord, sdk.OpRun, []byte(`{"plugin_input":{"marker":"dispatch"}}`), nil)
		if err != nil {
			return nil, fmt.Errorf("exampledispatch: invoke-provider %q: %w", in.TargetWord, err)
		}
		out["provider_result"] = pres
	}

	// (2) host-build: request a host-side build of a candy's plugin binary.
	if in.BuildCandyDir != "" && in.BuildName != "" {
		spec, _ := json.Marshal(map[string]string{"candy_dir": in.BuildCandyDir, "name": in.BuildName})
		bres, err := exec.HostBuild(ctx, "plugin-binary", spec)
		if err != nil {
			return nil, fmt.Errorf("exampledispatch: host-build: %w", err)
		}
		out["build_result"] = bres
	}

	res, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	return &pb.InvokeReply{ResultJson: res}, nil
}

type meta struct {
	pb.UnimplementedPluginMetaServer
}

// Describe advertises verb:exampledispatch. (The F10 reverse legs are the SDK Executor's
// InvokeProvider/HostBuild — no extra capability surface; the verb is driven WITH an executor.)
func (meta) Describe(context.Context, *pb.Empty) (*pb.Capabilities, error) {
	return sdk.BuildCapabilities(calver,
		[]sdk.ProvidedCapability{
			{Class: "verb", Word: "exampledispatch", InputDef: "#ExampledispatchInput"},
			{Class: "verb", Word: "exampledispatchpeer"}, // the OUT-OF-PROCESS InvokeProvider target (no plugin_input)
		},
		schemaFS, "schema")
}
