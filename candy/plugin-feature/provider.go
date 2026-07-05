package feature

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
)

// provider.go — the Invoke(OpRun) surface for the COMPILED-IN command:feature placement. The host's
// command dispatch (dispatchInProcCommand) invokes this in-process with the pass-through args + the
// threaded in-proc reverse channel, so runFeatureCLI can HostBuild the "feature" enumeration seam whose
// loader stays in core. (The out-of-process placement fork/execs the binary → CliMain, which has no
// reverse channel and errors — feature is compiled-in.)

type provider struct{ pb.UnimplementedProviderServer }

// Invoke runs `charly feature` in-process for the compiled-in command:feature placement. It RETURNS the
// error so a non-zero exit (validation failures) propagates.
func (provider) Invoke(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	if req.GetOp() != sdk.OpRun {
		return nil, fmt.Errorf("plugin-feature: unsupported op %q (want %q)", req.GetOp(), sdk.OpRun)
	}
	var in struct {
		Args []string `json:"args"`
	}
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &in); err != nil {
			return nil, fmt.Errorf("plugin-feature: decode args: %w", err)
		}
	}
	exec, err := sdk.ExecutorForInvoke(ctx, req.GetExecutorBrokerId())
	if err != nil {
		return nil, fmt.Errorf("plugin-feature: reverse-channel executor: %w", err)
	}
	if rerr := runFeatureCLI(ctx, exec, in.Args); rerr != nil {
		return nil, rerr
	}
	return &pb.InvokeReply{}, nil
}
