package status

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk/spec"
)

// provider.go — the Invoke surface for the COMPILED-IN command:status placement. The host's
// command dispatch (provider_command_external.go dispatchInProcCommand) invokes OpRun in-process
// with the pass-through args + the threaded in-proc reverse channel, so runStatusCLI can
// HostBuild the collection engine that stays in core. It ALSO serves sdk.OpStatusCollect — the
// programmatic status-collection API a peer plugin reaches via InvokeProvider — returning the
// overlaid []spec.DeploymentStatus as ResultJson (no render).

type provider struct{ pb.UnimplementedProviderServer }

// Invoke dispatches by op: OpRun runs `charly status` in-process for the compiled-in placement
// (decodes the pass-through args, recovers the reverse-channel executor, runs the CLI — which
// reaches the collection engine via HostBuild); OpStatusCollect is the programmatic leg (no CLI
// parsing, no render — just the overlaid status rows as JSON).
func (provider) Invoke(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	exec, err := sdk.ExecutorForInvoke(ctx, req.GetExecutorBrokerId())
	if err != nil {
		return nil, fmt.Errorf("plugin-status: reverse-channel executor: %w", err)
	}

	switch req.GetOp() {
	case sdk.OpRun:
		var in struct {
			Args []string `json:"args"`
		}
		if len(req.GetParamsJson()) > 0 {
			if uerr := json.Unmarshal(req.GetParamsJson(), &in); uerr != nil {
				return nil, fmt.Errorf("plugin-status: decode args: %w", uerr)
			}
		}
		if rerr := runStatusCLI(ctx, exec, in.Args); rerr != nil {
			return nil, rerr
		}
		return &pb.InvokeReply{}, nil

	case sdk.OpStatusCollect:
		var in spec.StatusSubstrateRequest
		if len(req.GetParamsJson()) > 0 {
			if uerr := json.Unmarshal(req.GetParamsJson(), &in); uerr != nil {
				return nil, fmt.Errorf("plugin-status: decode status-collect request: %w", uerr)
			}
		}
		reply, herr := hostStatusSubstrate(ctx, exec, in)
		if herr != nil {
			return nil, herr
		}
		// The programmatic leg always resolves the declared tree WITHOUT live-probing
		// (nested=false) — it has no CLI flag to carry the intent, and no current
		// peer caller needs the live multi-hop probe (that's the interactive `--nested`
		// leg's job, wired through runStatusCLI/command.go instead).
		roots, rerr := buildStatusRootsTree(exec, ctx, false)
		if rerr != nil {
			return nil, rerr
		}
		overlaid := applyNestedOverlay(reply.Rows, roots)
		resultJSON, merr := json.Marshal(overlaid)
		if merr != nil {
			return nil, fmt.Errorf("plugin-status: marshal status-collect result: %w", merr)
		}
		return &pb.InvokeReply{ResultJson: resultJSON}, nil

	default:
		return nil, fmt.Errorf("plugin-status: unsupported op %q (want %q or %q)", req.GetOp(), sdk.OpRun, sdk.OpStatusCollect)
	}
}
