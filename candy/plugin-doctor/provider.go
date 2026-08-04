package doctor

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/spec/proto"
)

// provider.go — the Invoke surface for BOTH compiled-in capabilities this candy declares
// (plugin.go's NewMeta): command:doctor (OpRun) and verb:freshness-guard (OpPreflight, F9). The
// host's command dispatch (provider_command_external.go dispatchInProcCommand) invokes doctor
// in-process with the pass-through args + the threaded in-proc reverse channel, so runDoctorCLI's
// peer sdk.Executor.InvokeProvider calls (hostfacts.go — verb:gpu, verb:credential) can reach the
// host's registry. (The out-of-process placement fork/execs the binary → CliMain, which passes a
// nil executor — those two peer calls degrade to zero values rather than erroring; see
// hostfacts.go.) freshness-guard is invoked by the kernel's preflight-phase pre-pass
// (runPreflightPhase, charly/preflight_phase.go) before ANY command dispatch — see freshness.go.

type provider struct{ pb.UnimplementedProviderServer }

// Invoke dispatches by (req.GetReserved(), req.GetOp()) — the two capabilities this candy
// declares share this one Invoke method, as every compiled-in candy's providers do.
func (provider) Invoke(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	switch req.GetReserved() {
	case "doctor":
		return invokeDoctor(ctx, req)
	case "freshness-guard":
		return invokeFreshnessGuard(req)
	default:
		return nil, fmt.Errorf("plugin-doctor: unknown reserved word %q", req.GetReserved())
	}
}

// invokeDoctor runs `charly doctor` in-process for the compiled-in command:doctor placement: it
// decodes the pass-through args, recovers the reverse-channel executor from the ctx (threaded by
// the host command dispatch), and runs the doctor logic. It RETURNS the error so a non-zero exit
// propagates (a required-dependency failure).
func invokeDoctor(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
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
