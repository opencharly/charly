package ssh

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/spec/proto"
)

// provider.go — the Invoke(OpRun) surface for the compiled-in command:ssh placement. The host's
// command dispatch (dispatchInProcCommand) invokes this in-process with the pass-through args + the
// threaded in-proc reverse channel; the kong-parsed SshCmd handlers reach verb:libvirt through the
// stashed executor (setCommandContext → tunnel.go's cmdExec).

// cmdCtx / cmdExec carry the Invoke(OpRun) reverse-channel handle to the tunnel handler's
// InvokeProvider(verb:libvirt) call.
var (
	cmdCtx  context.Context
	cmdExec *sdk.Executor
)

// setCommandContext stashes the reverse-channel executor for the duration of one `charly ssh …`
// dispatch. Called once at the top of command:ssh's Invoke(OpRun).
func setCommandContext(ctx context.Context, ex *sdk.Executor) {
	cmdCtx = ctx
	cmdExec = ex
}

type provider struct{ pb.UnimplementedProviderServer }

// Invoke runs `charly ssh …` in-process: decode the pass-through args, recover the reverse-channel
// executor, stash it for the tunnel handler, and kong-parse + run the SshCmd tree.
func (provider) Invoke(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	if req.GetOp() != sdk.OpRun {
		return nil, fmt.Errorf("plugin-ssh: unsupported op %q (want %q)", req.GetOp(), sdk.OpRun)
	}
	var in struct {
		Args []string `json:"args"`
	}
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &in); err != nil {
			return nil, fmt.Errorf("plugin-ssh: decode args: %w", err)
		}
	}
	exec, err := sdk.ExecutorForInvoke(ctx, req.GetExecutorBrokerId())
	if err != nil {
		return nil, fmt.Errorf("plugin-ssh: reverse-channel executor: %w", err)
	}
	setCommandContext(ctx, exec)
	var cli SshCmd
	if rerr := sdk.RunInProcCLI("ssh", &cli, in.Args); rerr != nil {
		return nil, rerr
	}
	return &pb.InvokeReply{}, nil
}
