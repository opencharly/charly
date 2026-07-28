package cmd

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
)

// provider.go — the Invoke(OpRun) surface for the compiled-in command:cmd placement. The host's
// command dispatch (dispatchInProcCommand) invokes this in-process with the pass-through args + the
// threaded in-proc reverse channel; the kong-parsed CmdCmd handler drives the "pod-cmd" host-builder
// (via the stashed executor) and sends the notification itself.

// cmdCtx / cmdExec carry the Invoke(OpRun) reverse-channel handle to the CmdCmd handler's
// HostBuild("pod-cmd") call.
var (
	cmdCtx  context.Context
	cmdExec *sdk.Executor
)

// setCommandContext stashes the reverse-channel executor for the duration of one `charly cmd …`
// dispatch. Called once at the top of command:cmd's Invoke(OpRun).
func setCommandContext(ctx context.Context, ex *sdk.Executor) {
	cmdCtx = ctx
	cmdExec = ex
}

type provider struct{ pb.UnimplementedProviderServer }

// Invoke runs `charly cmd …` in-process: decode the pass-through args, recover the reverse-channel
// executor, stash it for the CmdCmd handler, and kong-parse + run the command. A command's own
// non-zero exit propagates as the *sdk.ExitCodeError the handler returns (the host maps it to the
// process exit code).
func (provider) Invoke(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	if req.GetOp() != sdk.OpRun {
		return nil, fmt.Errorf("plugin-cmd: unsupported op %q (want %q)", req.GetOp(), sdk.OpRun)
	}
	var in struct {
		Args []string `json:"args"`
	}
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &in); err != nil {
			return nil, fmt.Errorf("plugin-cmd: decode args: %w", err)
		}
	}
	exec, err := sdk.ExecutorForInvoke(ctx, req.GetExecutorBrokerId())
	if err != nil {
		return nil, fmt.Errorf("plugin-cmd: reverse-channel executor: %w", err)
	}
	setCommandContext(ctx, exec)
	var cli CmdCmd
	if rerr := sdk.RunInProcCLI("cmd", &cli, in.Args); rerr != nil {
		return nil, rerr
	}
	return &pb.InvokeReply{}, nil
}
