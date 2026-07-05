package doctor

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
)

// provider.go — the Invoke(OpRun) surface for the COMPILED-IN command:doctor placement. The host's
// command dispatch (provider_command_external.go dispatchInProcCommand) invokes this in-process with
// the pass-through args + the threaded in-proc reverse channel, so runDoctorCLI can HostBuild the
// "hostprobe" host seam (the GPU/VFIO/device detection primitives + credentialHealth + the core
// install-hint/device tables) that stays in core. (The out-of-process placement fork/execs the binary →
// CliMain, which has no reverse channel and errors — doctor is compiled-in.)

type provider struct{ pb.UnimplementedProviderServer }

// Invoke runs `charly doctor` in-process for the compiled-in command:doctor placement: it decodes the
// pass-through args, recovers the reverse-channel executor from the ctx (threaded by the host command
// dispatch), and runs the doctor logic — which reaches the "hostprobe" host seam via HostBuild. It
// RETURNS the error so a non-zero exit propagates (a required-dependency failure).
func (provider) Invoke(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	if req.GetOp() != sdk.OpRun {
		return nil, fmt.Errorf("plugin-doctor: unsupported op %q (want %q)", req.GetOp(), sdk.OpRun)
	}
	var in struct {
		Args []string `json:"args"`
	}
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &in); err != nil {
			return nil, fmt.Errorf("plugin-doctor: decode args: %w", err)
		}
	}
	exec, err := sdk.ExecutorForInvoke(ctx, req.GetExecutorBrokerId())
	if err != nil {
		return nil, fmt.Errorf("plugin-doctor: reverse-channel executor: %w", err)
	}
	if rerr := runDoctorCLI(ctx, exec, in.Args); rerr != nil {
		return nil, rerr
	}
	return &pb.InvokeReply{}, nil
}
