package settings

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
)

// provider.go — the Invoke(OpRun) surface for the COMPILED-IN command:settings placement. The host's
// command dispatch (provider_command_external.go dispatchInProcCommand) invokes this in-process with
// the pass-through args + the threaded in-proc reverse channel, so runSettingsCLI can HostBuild the
// config subsystem that stays in core. (The out-of-process placement fork/execs the binary → CliMain,
// which has no reverse channel and errors — settings is compiled-in.)

type provider struct{ pb.UnimplementedProviderServer }

// Invoke runs `charly settings` in-process for the compiled-in command:settings placement: it decodes
// the pass-through args, recovers the reverse-channel executor from the ctx, and runs the settings
// logic — which reaches the config subsystem via HostBuild. It RETURNS the error so a non-zero exit
// propagates.
func (provider) Invoke(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	if req.GetOp() != sdk.OpRun {
		return nil, fmt.Errorf("plugin-settings: unsupported op %q (want %q)", req.GetOp(), sdk.OpRun)
	}
	var in struct {
		Args []string `json:"args"`
	}
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &in); err != nil {
			return nil, fmt.Errorf("plugin-settings: decode args: %w", err)
		}
	}
	exec, err := sdk.ExecutorForInvoke(ctx, req.GetExecutorBrokerId())
	if err != nil {
		return nil, fmt.Errorf("plugin-settings: reverse-channel executor: %w", err)
	}
	if rerr := runSettingsCLI(ctx, exec, in.Args); rerr != nil {
		return nil, rerr
	}
	return &pb.InvokeReply{}, nil
}
