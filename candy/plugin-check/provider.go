package check

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/spec/proto"
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
	// verb:check-resolve (OpResolve) — the internal venue-classification capability the host's
	// floor reverse-legs call (#118 check broker-envelope-out); routed here, never the command path.
	if req.GetOp() == sdk.OpResolve {
		return resolveVenueForHost(ctx, req)
	}
	// verb:check-resolve's other legs (#55 W3 B7): the relocated CheckContext.ResolveEndpoint/
	// ResolveImageLabel resolution bodies + their shared cleanup-drain signal.
	switch req.GetOp() {
	case sdk.OpResolveEndpoint:
		return resolveEndpointForHost(ctx, req)
	case sdk.OpResolveImageLabel:
		return resolveImageLabelForHost(ctx, req)
	case sdk.OpDrainEndpointCleanups:
		return drainEndpointCleanupsForHost(ctx, req)
	}
	// verb:… — no; command:check's DEPLOY-VERIFY drive (OpVerifyChecks, #55 CHECK-ENGINE cone
	// Unit 2): run a deploy-scope check pass plugin-side over the threaded live venue, so charly
	// core's checkrun.go + planrun_adapter.go shed their sdk/kit imports. Routed here, never the
	// command CLI path (which parses `charly check …` args).
	if req.GetOp() == sdk.OpVerifyChecks {
		return verifyChecksForHost(ctx, req)
	}
	if req.GetOp() != sdk.OpRun {
		return nil, fmt.Errorf("plugin-check: unsupported op %q (want %q, %q, %q, %q, %q, or %q)",
			req.GetOp(), sdk.OpRun, sdk.OpResolve, sdk.OpResolveEndpoint, sdk.OpResolveImageLabel, sdk.OpDrainEndpointCleanups, sdk.OpVerifyChecks)
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
