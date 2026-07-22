// Package exampledispatch is the charly example plugin (an importable, dual-placement root package) proving the F10 reverse legs:
// during its own Invoke (run WITH a reverse channel), it calls BACK to the host to (1) INVOKE
// ANOTHER plugin's verb (sdk.Executor.InvokeProvider — plugin↔plugin via the host broker) and
// (2) request a HOST-BUILD (sdk.Executor.HostBuild — the host runs a registered host-builder),
// returning both results. This exercises the NESTED-BROKER round-trip (A's Invoke holds a broker;
// the host stands up a SECOND broker to dispatch the peer) generically — the generalization of the
// RunHostStep ExternalPlugin arm to any class/op + a standalone build request.
//
// It also proves the S1 venue-scoped-executor-session seam: the caller may optionally supply a
// VenueDescriptor (sdk.InvokeProviderOpts) alongside a PeerCmd, in which case the OOP peer
// (exampledispatchpeer) actually RunCaptures that command over whatever executor the host threads
// onto it — its own s.exec by default, or a FRESH executor the host materialized from the supplied
// descriptor when set. See plugin_dispatch_reverse_venue_test.go (charly core) for the regression
// suite driving this end-to-end.
package exampledispatch

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

const calver = "2026.181.0001"

// NewProvider returns the exampledispatch provider.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta advertises verb:exampledispatch (+ the OUT-OF-PROCESS InvokeProvider target
// exampledispatchpeer) via sdk.NewMeta → BuildCapabilities. (The F10 reverse legs are
// the SDK Executor's InvokeProvider/HostBuild — no extra capability surface; the verb is
// driven WITH an executor.)
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta(calver,
		[]sdk.ProvidedCapability{
			{Class: "verb", Word: "exampledispatch", InputDef: "#ExampledispatchInput"},
			{Class: "verb", Word: "exampledispatchpeer"}, // the OUT-OF-PROCESS InvokeProvider target (no plugin_input)
		},
		schemaFS)
}

type provider struct{ pb.UnimplementedProviderServer }

// dispatchInput is the op's plugin_input: which peer verb to invoke + which candy binary to host-build.
type dispatchInput struct {
	TargetWord    string `json:"target_word"`     // a verb word the host resolves + Invokes (plugin↔plugin)
	BuildCandyDir string `json:"build_candy_dir"` // a candy dir the host builds a plugin binary for (host-build)
	BuildName     string `json:"build_name"`
	// PeerCmd, when set, is forwarded to exampledispatchpeer as its own peerInput.PeerCmd — the
	// peer then RunCaptures it over whatever executor the host threads onto it (S1 proof).
	PeerCmd string `json:"peer_cmd,omitempty"`
	// VenueDescriptor, when set, rides InvokeProviderOpts on the InvokeProvider call to
	// TargetWord — the host re-materializes a FRESH executor from it and threads THAT onto the
	// target instead of this plugin's own s.exec (S1: the venue-scoped-executor-session seam).
	VenueDescriptor *spec.VenueDescriptor `json:"venue_descriptor,omitempty"`
}

// peerInput is exampledispatchpeer's own plugin_input: an optional command to RunCapture over
// whatever executor the host threads onto this Invoke.
type peerInput struct {
	PeerCmd string `json:"peer_cmd,omitempty"`
}

// Invoke dispatches by word: the PEER verb (exampledispatchpeer) is the OUT-OF-PROCESS
// InvokeProvider target other plugins (and the host's own S1/S2 regression tests) reach over the
// nested broker; the dispatcher verb (exampledispatch) exercises all three F10/S1 legs.
func (provider) Invoke(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	if req.GetReserved() == "exampledispatchpeer" {
		var in peerInput
		if len(req.GetParamsJson()) > 0 {
			// Tolerate the legacy no-field payload (`{"plugin_input":{"marker":"dispatch"}}`) —
			// it simply decodes to a zero-value peerInput.
			_ = json.Unmarshal(req.GetParamsJson(), &in)
		}
		if in.PeerCmd == "" {
			// The plain echo leg: a deterministic marker proving the nested broker reached this
			// peer at all (no executor exercised — the pre-S1 F10 proof stays unchanged).
			out, _ := json.Marshal(map[string]string{"status": "pass", "message": "peer-reached"})
			return &pb.InvokeReply{ResultJson: out}, nil
		}
		// The S1 proof leg: actually run PeerCmd over whatever executor the host threaded onto
		// THIS Invoke (its own default s.exec, or a descriptor-materialized fresh one — the peer
		// cannot tell which; that is the whole point of the seam).
		exec, err := sdk.ExecutorFromInvoke(req.GetExecutorBrokerId())
		if err != nil {
			out, _ := json.Marshal(map[string]string{"status": "fail", "message": "no-executor: " + err.Error()})
			return &pb.InvokeReply{ResultJson: out}, nil
		}
		stdout, stderr, exit, rerr := exec.RunCapture(ctx, in.PeerCmd)
		if rerr != nil {
			out, _ := json.Marshal(map[string]any{"status": "fail", "message": rerr.Error(), "stdout": stdout, "stderr": stderr, "exit": exit})
			return &pb.InvokeReply{ResultJson: out}, nil
		}
		out, _ := json.Marshal(map[string]any{"status": "pass", "message": "peer-executed", "stdout": stdout, "stderr": stderr, "exit": exit})
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
	// dispatches over a nested broker). A valid #*Input for the reference verb is {"marker": …};
	// PeerCmd/VenueDescriptor (S1) instead exercise exampledispatchpeer's RunCapture leg above.
	if in.TargetWord != "" {
		params := []byte(`{"plugin_input":{"marker":"dispatch"}}`)
		if in.PeerCmd != "" {
			pj, perr := json.Marshal(peerInput{PeerCmd: in.PeerCmd})
			if perr != nil {
				return nil, fmt.Errorf("exampledispatch: marshal peer input: %w", perr)
			}
			params = pj
		}
		opts := sdk.InvokeProviderOpts{VenueDescriptor: in.VenueDescriptor}
		pres, err := exec.InvokeProvider(ctx, "verb", in.TargetWord, sdk.OpRun, params, nil, opts)
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
