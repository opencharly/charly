// Package examplecommand is the importable form of the reference charly COMMAND-class plugin,
// usable in BOTH placements (F8): COMPILED INTO charly in-process (charly imports this package +
// registers NewProvider()/NewMeta() via plugins_generated.go; `charly examplecommand <args>`
// dispatches IN-PROC via Invoke(OpRun) — dispatchInProcCommand) OR served OUT-OF-PROCESS (the
// cmd/serve shim; charly fork/execs the binary in CLI mode → CliMain). Both placements run the
// SAME runCommand (print the joined args), so the command behaves identically regardless of
// placement — the placement-invisible command, the command-class analogue of
// candy/plugin-example-external. The dynamic Kong grammar (pass-through Args) is built host-side
// (externalCommandHolder) for both placements; only the dispatch transport differs.
package examplecommand

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
)

const calver = "2026.181.0001"

// NewProvider returns the command provider for in-proc registration (compiled-in) or out-of-proc serving.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta advertises command:examplecommand via sdk.NewMeta → BuildCapabilities so the
// COMPILED-IN path registers it as a command provider (buildUnitInProc → inprocProvider
// Class=command; the host builds its dynamic Kong grammar + dispatches Invoke(OpRun)).
// The served schema carries no #*Input def — a command's args are pass-through CLI
// tokens, not a structured plugin_input — so the capability has no InputDef.
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta(calver,
		[]sdk.ProvidedCapability{{Class: "command", Word: "examplecommand"}},
		nil)
}

// CliMain is the OUT-OF-PROCESS CLI-mode entry (charly fork/execs the binary with the pass-through
// tokens after `charly examplecommand`). It runs the SAME effect as the in-proc Invoke(OpRun) path.
func CliMain(args []string) int {
	runCommand(args)
	return 0
}

// runCommand is the command's ONE effect, shared by both placements: print the joined args to
// stdout (charly's own stdout when compiled-in/in-proc; the fork/exec'd process's stdout OOP), so
// a test can assert the command ran, which args it received, AND that it reached a real stdout.
func runCommand(args []string) {
	fmt.Println(strings.Join(args, " "))
}

type provider struct{ pb.UnimplementedProviderServer }

// Invoke handles OpRun for the COMPILED-IN (in-proc) dispatch: decode the pass-through {args} and
// run the command effect in charly's own process. (Out-of-process dispatch is fork/exec → CliMain,
// never this gRPC path.)
func (provider) Invoke(_ context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	if req.GetOp() != sdk.OpRun {
		return nil, fmt.Errorf("examplecommand: unsupported op %q (only %q)", req.GetOp(), sdk.OpRun)
	}
	var in struct {
		Args []string `json:"args"`
	}
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &in); err != nil {
			return nil, fmt.Errorf("examplecommand: decode args: %w", err)
		}
	}
	runCommand(in.Args)
	return &pb.InvokeReply{}, nil
}
