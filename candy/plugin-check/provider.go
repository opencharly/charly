package check

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
)

// provider.go — the Invoke(OpRun) surface for the COMPILED-IN command:check placement. The host's
// command dispatch (provider_command_external.go dispatchInProcCommand) invokes this in-process with
// the pass-through args + the threaded in-proc reverse channel, so the kong-parsed CheckCmd handlers
// reach the host check-run / config / cli / agent seams. (The out-of-process placement fork/execs the
// binary → CliMain, which has no reverse channel and errors — check is compiled-in.)

type provider struct{ pb.UnimplementedProviderServer }

// Invoke runs `charly check …` in-process for the compiled-in command:check placement: it decodes the
// pass-through args, recovers the reverse-channel executor from the ctx (threaded by the host command
// dispatch), stashes it for the deep CLI handlers (setCommandContext), and kong-parses + runs the
// CheckCmd tree. It RETURNS the error so a non-zero / check-fail exit propagates.
func (provider) Invoke(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	if req.GetOp() != sdk.OpRun {
		return nil, fmt.Errorf("plugin-check: unsupported op %q (want %q)", req.GetOp(), sdk.OpRun)
	}
	var in struct {
		Args []string `json:"args"`
	}
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &in); err != nil {
			return nil, fmt.Errorf("plugin-check: decode args: %w", err)
		}
	}
	exec, err := sdk.ExecutorForInvoke(ctx, req.GetExecutorBrokerId())
	if err != nil {
		return nil, fmt.Errorf("plugin-check: reverse-channel executor: %w", err)
	}
	setCommandContext(ctx, exec)
	if rerr := dispatchCheckCLI(in.Args); rerr != nil {
		return nil, mapCheckExitError(rerr)
	}
	return &pb.InvokeReply{}, nil
}

// mapCheckExitError wraps a check-command failure / skip in *sdk.ExitCodeError so the HOST (main()'s
// exit mapping) sets the goss/pytest PROCESS exit code across the module boundary — 2 for a
// checks-failure (CheckFailedError), 3 for a prerequisite skip (CheckSkippedError). The host cannot
// classify the plugin's OWN error types, so this boundary translation is required. Any other error
// propagates verbatim (exit 1, the host default).
func mapCheckExitError(err error) error {
	var cf *CheckFailedError
	if errors.As(err, &cf) {
		return &sdk.ExitCodeError{Code: sdk.CheckFailExitCode, Err: err}
	}
	var cs *CheckSkippedError
	if errors.As(err, &cs) {
		return &sdk.ExitCodeError{Code: sdk.CheckSkippedExitCode, Err: err}
	}
	return err
}
