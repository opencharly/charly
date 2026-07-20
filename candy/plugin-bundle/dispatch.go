package bundle

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
)

// dispatch.go — the K4-C deploy-DISPATCH leg of command:bundle (P13-KERNEL spike #1). The host's
// deployAddCmd.dispatchNode, for the root-level (non-nested) Add path, Invokes this OpDispatch
// leg instead of calling ResolveTarget().Add() directly in-process — a PLUGIN-INITIATED call
// that nests a SECOND out-of-process substrate dispatch through HostBuild("deploy-dispatch"),
// proving the reverse-channel broker threads correctly when the outer call originates from the
// plugin (rather than the host, as OpCompile's HostBuild("resolved-project") call does today).
// This handler is a thin relay (R3 — mirrors runBundleCompile's shape): recover the executor,
// stash it (setCommandContext, so hostDeploySeam can reach it), and forward the request VERBATIM
// to HostBuild("deploy-dispatch"), where hostBuildDeployDispatch reconstructs the config +
// DeployContext and runs the actual ResolveTarget().Add(). IMPORT-PURITY: imports ONLY
// github.com/opencharly/sdk (proto is a subpackage); never charly/.

// runBundleDispatch serves command:bundle's Invoke(OpDispatch): recover the executor, stash the
// reverse-channel handle, and relay the request VERBATIM to HostBuild("deploy-dispatch").
func runBundleDispatch(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	exec, err := sdk.ExecutorForInvoke(ctx, req.GetExecutorBrokerId())
	if err != nil {
		return nil, fmt.Errorf("bundle dispatch: reach host reverse channel: %w", err)
	}
	setCommandContext(ctx, exec)
	if len(req.GetParamsJson()) == 0 {
		return nil, fmt.Errorf("bundle dispatch: empty request")
	}
	// json.RawMessage.MarshalJSON returns itself byte-for-byte, so hostDeploySeam's re-marshal
	// forwards the already-encoded request VERBATIM.
	if err := hostDeploySeam("deploy-dispatch", json.RawMessage(req.GetParamsJson())); err != nil {
		return nil, err
	}
	return &pb.InvokeReply{}, nil
}
