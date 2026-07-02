// Package preempt is the importable form of charly's RESOURCE-ARBITER plugin (cutover C9). It
// serves TWO capabilities:
//
//   - verb:arbiter — the exclusive/shared resource arbiter (the 1225-LOC logic moved OUT of
//     charly core: acquire/release, stop+restore holders, the crash-safe lease ledger, GPU
//     poisoning, the vfio<->nvidia mode arbitration). COMPILED-IN + dispatched IN-PROC by the
//     in-core proxy (charly/preempt.go newResourceArbiter → resolve(verb:arbiter)+Invoke); the
//     arbiter reaches its host dependencies (config, VM/pod lifecycle, GPU flip) over the
//     ExecutorService.HostArbiter reverse channel (arbiter.go).
//   - command:preempt — the operator `charly preempt status`/`restore` CLI, unchanged: it shells
//     back through the hidden in-core `charly __preempt-status`/`__preempt-restore` verbs (which
//     now drive the arbiter via the proxy). COMPILED-IN → dispatched in-proc via Invoke(OpRun);
//     the cmd/serve binary serves the out-of-process fork/exec placement (CliMain).
//
// PLACEMENT — COMPILED-IN (listed in the embedded charly/charly.yml compiled_plugins:). The
// arbiter is on the deploy/vm/check hot paths + needs the local lease ledger + config, so in-proc
// is the right placement (like plugin-gpu). The reverse channel is in-proc (no gRPC broker) —
// the SAME dispatchBuild pattern.
package preempt

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"

	pb "github.com/opencharly/charly/charly/plugin/proto"
	"github.com/opencharly/charly/charly/plugin/sdk"
	"github.com/opencharly/charly/charly/spec"
)

//go:embed schema/*.cue
var schemaFS embed.FS

const calver = "2026.183.0000"

// NewProvider returns the arbiter+command provider for in-proc registration or out-of-proc serving.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta returns the capability/schema describer.
func NewMeta() pb.PluginMetaServer { return &meta{} }

// CliMain is the OUT-OF-PROCESS command-dispatch entry (charly fork/execs the binary with the
// pass-through tokens after `charly preempt`). It runs the SAME shellback CLI as the in-proc
// command Invoke path.
func CliMain(args []string) int { return runPreemptCLI(args) }

type provider struct{ pb.UnimplementedProviderServer }

// Invoke serves BOTH words on OpRun, discriminated by req.Reserved:
//   - "arbiter": decode the action-tagged spec.ArbiterInvokeInput, run the arbiter (wired to the
//     host reverse channel via sdk.ExecutorForInvoke), and echo its spec.ArbiterInvokeReply.
//   - "preempt": the COMPILED-IN command dispatch — decode the pass-through {args} and run the
//     shellback CLI in charly's own process (stdio inherited).
func (provider) Invoke(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	if req.GetOp() != sdk.OpRun {
		return nil, fmt.Errorf("preempt: unsupported op %q (only %q)", req.GetOp(), sdk.OpRun)
	}
	switch req.GetReserved() {
	case "arbiter":
		exec, err := sdk.ExecutorForInvoke(ctx, req.GetExecutorBrokerId())
		if err != nil {
			return nil, fmt.Errorf("arbiter: reach host reverse channel: %w", err)
		}
		var in spec.ArbiterInvokeInput
		if len(req.GetParamsJson()) > 0 {
			if err := json.Unmarshal(req.GetParamsJson(), &in); err != nil {
				return nil, fmt.Errorf("arbiter: decode input: %w", err)
			}
		}
		reply := invokeArbiter(ctx, exec, in)
		out, err := json.Marshal(reply)
		if err != nil {
			return nil, err
		}
		return &pb.InvokeReply{ResultJson: out}, nil
	case "preempt":
		var in struct {
			Args []string `json:"args"`
		}
		if len(req.GetParamsJson()) > 0 {
			if err := json.Unmarshal(req.GetParamsJson(), &in); err != nil {
				return nil, fmt.Errorf("preempt command: decode args: %w", err)
			}
		}
		runPreemptCLI(in.Args)
		return &pb.InvokeReply{}, nil
	default:
		return nil, fmt.Errorf("preempt: unknown word %q (want arbiter|preempt)", req.GetReserved())
	}
}

type meta struct {
	pb.UnimplementedPluginMetaServer
}

// Describe advertises verb:arbiter (dispatched host-side via the in-core proxy, no authored
// plugin_input → no InputDef) + command:preempt (pass-through CLI args → no InputDef). The
// self-contained #PreemptPlugin schema satisfies the non-empty-schema load gate.
func (meta) Describe(context.Context, *pb.Empty) (*pb.Capabilities, error) {
	return sdk.BuildCapabilities(calver,
		[]sdk.ProvidedCapability{
			{Class: "verb", Word: "arbiter"},
			{Class: "command", Word: "preempt"},
		},
		schemaFS, "schema")
}
