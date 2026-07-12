package bundle

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/alecthomas/kong"
	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
)

// command.go is the command:bundle leg — the `charly bundle …` CLI, COMPILED-IN (F8). It dispatches
// IN-PROC via Invoke(OpRun): the reverse-channel executor is stashed (setCommandContext) so the
// moved BundleCmd handlers reach their host seams (deploy-add / deploy-del / deploy-from-box /
// deploy-config), then the pass-through args are kong-parsed into the BundleCmd
// tree and run. Because in-proc dispatch runs in charly's OWN process, the handlers inherit charly's
// real stdin/stdout/stderr/TTY natively — which keeps `charly bundle add`'s interactive prompts and
// dry-run output working exactly as before. Mirrors candy/plugin-vm/command.go.

// Invoke handles OpRun for the COMPILED-IN (in-proc) dispatch of command:bundle.
func (provider) Invoke(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	if req.GetOp() != sdk.OpRun {
		return nil, fmt.Errorf("bundle: unsupported op %q (only %q)", req.GetOp(), sdk.OpRun)
	}
	return runBundleCommand(ctx, req)
}

// runBundleCommand serves command:bundle's Invoke(OpRun): recover the executor, decode the
// pass-through args, and run the BundleCmd tree (the plugin-vm command-dispatch pattern).
func runBundleCommand(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	exec, err := sdk.ExecutorForInvoke(ctx, req.GetExecutorBrokerId())
	if err != nil {
		return nil, fmt.Errorf("bundle command: reach host reverse channel: %w", err)
	}
	setCommandContext(ctx, exec)
	var in struct {
		Args []string `json:"args"`
	}
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &in); err != nil {
			return nil, fmt.Errorf("bundle command: decode args: %w", err)
		}
	}
	if rerr := dispatchBundleCLI(in.Args); rerr != nil {
		return nil, rerr
	}
	return &pb.InvokeReply{}, nil
}

// dispatchBundleCLI kong-parses the pass-through args into the BundleCmd tree and runs the selected
// leaf.
func dispatchBundleCLI(args []string) error {
	var cli BundleCmd
	parser, err := kong.New(&cli, kong.Name("bundle"), kong.Exit(func(int) {}))
	if err != nil {
		return err
	}
	kctx, err := parser.Parse(args)
	if err != nil {
		return err
	}
	return kctx.Run()
}

// CliMain is the OUT-OF-PROCESS command entry — unreachable in the canonical compiled-in placement.
// command:bundle's handlers reach the host reverse channel (the deploy-add/del/from-box dispatch +
// the deploy-config seam), which is unavailable out-of-process, so this errors (like plugin-vm's CliMain).
func CliMain(_ []string) int {
	fmt.Fprintln(os.Stderr, "charly bundle: requires compiled-in placement (the command's host reverse channel is unavailable out-of-process)")
	return 1
}
