// Package preempt is the importable form of charly's RESOURCE-ARBITER plugin (cutover C9). It
// serves TWO capabilities:
//
//   - verb:arbiter — the exclusive/shared resource arbiter (the 1225-LOC logic moved OUT of
//     charly core: acquire/release, stop+restore holders, the crash-safe lease ledger, GPU
//     poisoning, the vfio<->nvidia mode arbitration). COMPILED-IN + dispatched IN-PROC by the
//     in-core proxy (charly/preempt.go newResourceArbiter → resolve(verb:arbiter)+Invoke); the
//     arbiter reaches its host dependencies over TWO generic reverse legs (arbiter.go): the VM/
//     pod lifecycle + GPU driver flip via sdk.Executor.InvokeProvider (FLOOR-SLIM-proper Unit-8,
//     holder_dispatch.go), and its project deploy tree + resources via
//     sdk.Executor.HostBuild("resolved-project") (K1-unblock wave 1, retiring the former bespoke
//     ExecutorService.HostArbiter reverse RPC entirely).
//   - command:preempt — the operator `charly preempt status`/`restore` CLI. It OWNS the CLI grammar +
//     the lease-table formatting and reaches its OWN peer capability verb:arbiter DIRECTLY via
//     InvokeProvider over the reverse channel (no hidden `__preempt-*` forward, no in-core proxy hop —
//     command:preempt → InvokeProvider → verb:arbiter). COMPILED-IN → dispatched in-proc via
//     Invoke(OpRun); out-of-process CliMain errors (the reverse channel is unavailable there).
//
// PLACEMENT — COMPILED-IN (listed in the embedded charly/charly.yml compiled_plugins:). The
// arbiter is on the deploy/vm/check hot paths + needs the local lease ledger + config, so in-proc
// is the right placement (like plugin-gpu). The reverse channel is in-proc (no gRPC broker) —
// the SAME dispatchBuild pattern.
package preempt

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk/spec"
)

const calver = "2026.183.0000"

// NewProvider returns the arbiter+command provider for in-proc registration or out-of-proc serving.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta advertises verb:arbiter (dispatched host-side via the in-core proxy, no authored
// plugin_input → no InputDef) + command:preempt (pass-through CLI args → no InputDef), plus
// the self-contained #PreemptPlugin schema (via sdk.NewMeta → BuildCapabilities) that
// satisfies the non-empty-schema load gate.
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta(calver,
		[]sdk.ProvidedCapability{
			{Class: "verb", Word: "arbiter"},
			{Class: "command", Word: "preempt"},
		},
		nil)
}

// CliMain is the OUT-OF-PROCESS command-dispatch entry (only reached when preempt is NOT compiled in).
// preempt reaches verb:arbiter over the reverse channel, unavailable out-of-process, so runPreemptCLI
// (with a nil executor) errors; the canonical placement is compiled-in (Invoke), where the reverse
// channel is threaded.
func CliMain(args []string) int {
	if err := runPreemptCLI(context.Background(), nil, args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

type provider struct{ pb.UnimplementedProviderServer }

// Invoke serves BOTH words on OpRun, discriminated by req.Reserved:
//   - "arbiter": decode the action-tagged spec.ArbiterInvokeInput, run the arbiter (wired to the
//     host reverse channel via sdk.ExecutorForInvoke), and echo its spec.ArbiterInvokeReply.
//   - "preempt": the COMPILED-IN command dispatch — decode the pass-through {args}, recover the
//     reverse-channel executor, and run the CLI (which reaches verb:arbiter via InvokeProvider).
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
		exec, err := sdk.ExecutorForInvoke(ctx, req.GetExecutorBrokerId())
		if err != nil {
			return nil, fmt.Errorf("preempt command: reach host reverse channel: %w", err)
		}
		var in struct {
			Args []string `json:"args"`
		}
		if len(req.GetParamsJson()) > 0 {
			if err := json.Unmarshal(req.GetParamsJson(), &in); err != nil {
				return nil, fmt.Errorf("preempt command: decode args: %w", err)
			}
		}
		if rerr := runPreemptCLI(ctx, exec, in.Args); rerr != nil {
			return nil, rerr
		}
		return &pb.InvokeReply{}, nil
	default:
		return nil, fmt.Errorf("preempt: unknown word %q (want arbiter|preempt)", req.GetReserved())
	}
}
