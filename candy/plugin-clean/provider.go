package clean

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/spec/proto"
	"github.com/opencharly/spec/spec"
)

// provider.go — the Invoke surface for BOTH capabilities this plugin serves: the COMPILED-IN
// command:clean CLI placement, and verb:retention — the peer entry point into the
// retention ENGINE (retention.go) this plugin now OWNS (K1-alpha core-minimization relocation,
// mirroring verb:credential/verb:gpu/verb:tunnel). The host's command dispatch
// (provider_command_external.go dispatchInProcCommand) invokes command:clean in-process with the
// pass-through args + the threaded in-proc reverse channel, so runCleanCLI can HostBuild the ONE
// remaining host-coupled piece ("retention-defaults"). verb:retention is invoked directly by peer
// plugins (candy/plugin-box's post-build prune + `box list tags`, candy/plugin-check's post-run
// prune, all over InvokeProvider — NO core adapter remains, charly/retention_plugin.go is
// DELETED, #118) with an ALREADY-RESOLVED spec.RetentionRequest — no reverse channel needed for
// that leg, so it works whether clean is compiled-in or (in principle) out-of-process.

type provider struct{ pb.UnimplementedProviderServer }

// Invoke dispatches by the requested word: "clean" (command:clean, OpRun, pass-through CLI args)
// or "retention" (verb:retention, OpRun, a spec.RetentionRequest) — RETURNS the error for
// command:clean so a non-zero exit propagates; verb:retention never errors the RPC itself, it
// reports failure via spec.RetentionReply.Error (matching the former host_build_retention.go
// HostBuild-handler contract every caller already decodes).
func (provider) Invoke(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	if req.GetOp() != sdk.OpRun {
		return nil, fmt.Errorf("plugin-clean: unsupported op %q (want %q)", req.GetOp(), sdk.OpRun)
	}
	switch req.GetReserved() {
	case "retention":
		return invokeRetention(req)
	case "clean":
		return invokeCleanCLI(ctx, req)
	default:
		return nil, fmt.Errorf("plugin-clean: unsupported word %q", req.GetReserved())
	}
}

// invokeCleanCLI runs `charly clean` in-process for the compiled-in command:clean placement: it
// decodes the pass-through args, recovers the reverse-channel executor from the ctx (threaded by
// the host command dispatch), and runs the CLI.
func invokeCleanCLI(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	var in struct {
		Args []string `json:"args"`
	}
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &in); err != nil {
			return nil, fmt.Errorf("plugin-clean: decode args: %w", err)
		}
	}
	exec, err := sdk.ExecutorForInvoke(ctx, req.GetExecutorBrokerId())
	if err != nil {
		return nil, fmt.Errorf("plugin-clean: reverse-channel executor: %w", err)
	}
	if rerr := runCleanCLI(ctx, exec, in.Args); rerr != nil {
		return nil, rerr
	}
	return &pb.InvokeReply{}, nil
}

// invokeRetention decodes a spec.RetentionRequest, runs the local engine (retention.go), and
// marshals the spec.RetentionReply back — the verb:retention entry point every core adapter and
// peer plugin (candy/plugin-check) reaches via resolve+Invoke / InvokeProvider.
func invokeRetention(req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	var in spec.RetentionRequest
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &in); err != nil {
			return nil, fmt.Errorf("plugin-clean: decode retention request: %w", err)
		}
	}
	reply := runRetention(in)
	out, err := json.Marshal(reply)
	if err != nil {
		return nil, fmt.Errorf("plugin-clean: encode retention reply: %w", err)
	}
	return &pb.InvokeReply{ResultJson: out}, nil
}
