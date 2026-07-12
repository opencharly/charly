package vm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/alecthomas/kong"
	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
)

// command.go is the command:vm leg — the `charly vm …` CLI, COMPILED-IN (F8). It dispatches IN-PROC
// via Invoke(OpRun) (no more `__vm` re-exec): the reverse-channel executor is stashed
// (setCommandContext) so the moved VmCmd handlers reach their host seams (config-resolve/persist,
// egress, arbiter, gpu), then the pass-through args are kong-parsed into the VmCmd tree and run.
// Because in-proc dispatch runs in charly's OWN process, the handlers inherit charly's real
// stdin/stdout/stderr/TTY natively — which keeps `charly vm console` / `charly vm ssh` interactive.

// runVmCommand serves command:vm's Invoke(OpRun): recover the executor, decode the pass-through args,
// and run the VmCmd tree (the plugin-preempt command-dispatch pattern).
func runVmCommand(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	exec, err := sdk.ExecutorForInvoke(ctx, req.GetExecutorBrokerId())
	if err != nil {
		return nil, fmt.Errorf("vm command: reach host reverse channel: %w", err)
	}
	setCommandContext(ctx, exec)
	var in struct {
		Args []string `json:"args"`
	}
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &in); err != nil {
			return nil, fmt.Errorf("vm command: decode args: %w", err)
		}
	}
	if rerr := dispatchVmCLI(in.Args); rerr != nil {
		return nil, rerr
	}
	return &pb.InvokeReply{}, nil
}

// dispatchVmCLI kong-parses the pass-through args into the VmCmd tree and runs the selected leaf.
func dispatchVmCLI(args []string) error {
	var cli VmCmd
	parser, err := kong.New(&cli, kong.Name("vm"), kong.Exit(func(int) {}))
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
// command:vm's handlers reach the host reverse channel (config-resolve/persist, egress, arbiter,
// gpu), which is unavailable out-of-process, so this errors (like candy/plugin-preempt's CliMain).
func CliMain(_ []string) int {
	fmt.Fprintln(os.Stderr, "charly vm: requires compiled-in placement (the command's host reverse channel is unavailable out-of-process)")
	return 1
}
