package clean

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
)

// provider.go — the Invoke(OpRun) surface for the COMPILED-IN command:clean placement. The host's
// command dispatch (provider_command_external.go dispatchInProcCommand) invokes this in-process with
// the pass-through args + the threaded in-proc reverse channel, so runCleanCLI can HostBuild the
// shared "retention" engine that stays in core. (The out-of-process placement fork/execs the binary →
// cliMain, which has no reverse channel and errors — clean is compiled-in.)

type provider struct{ pb.UnimplementedProviderServer }

// Invoke runs `charly clean` in-process for the compiled-in command:clean placement: it decodes the
// pass-through args, recovers the reverse-channel executor from the ctx (threaded by the host command
// dispatch), and runs the clean logic — which reaches the shared retention engine via HostBuild. It
// RETURNS the error so a non-zero exit propagates.
func (provider) Invoke(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	if req.GetOp() != sdk.OpRun {
		return nil, fmt.Errorf("plugin-clean: unsupported op %q (want %q)", req.GetOp(), sdk.OpRun)
	}
	var in struct {
		Args []string `json:"args"`
	}
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &in); err != nil {
			return nil, fmt.Errorf("plugin-clean: decode args: %w", err)
		}
	}
	exec, err := sdk.ExecutorForInvoke(ctx, req.GetExecutorBrokerId())
	if err != nil {
		return nil, fmt.Errorf("plugin-clean: reverse-channel executor: %w", err)
	}
	if rerr := runCleanCLI(ctx, exec, in.Args); rerr != nil {
		return nil, rerr
	}
	return &pb.InvokeReply{}, nil
}
