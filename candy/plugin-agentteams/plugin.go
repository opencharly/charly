// Package agentteams is the importable form of charly's `agentteams` COMMAND
// plugin: a compiled-in `command:agentteams` management CLI for the AgentTeams
// controller REST API. A command provider dispatches via the pb Invoke(OpRun)
// envelope — decode the pass-through `{"args":[...]}` and kong-parse them into
// the AgentTeamsCmd tree (sdk.RunInProcCLI), so the handler runs in charly's OWN
// process with native stdio/TTY. Usable COMPILED-IN (NewProvider()/NewMeta() via
// plugins_generated.go) OR served OUT-OF-PROCESS by the cmd/serve shim — both
// placements run the SAME runCommand (placement-invisible, F8).
package agentteams

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/alecthomas/kong"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/spec/proto"
	"github.com/opencharly/spec/spec"
)

const calver = "2026.224.0600"

// NewProvider returns the command provider for in-proc registration (compiled-in)
// or out-of-proc serving.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta advertises command:agentteams via a lazy Describe: the kong CLIModel is
// reflected INSIDE Describe (agentTeamsMeta) rather than eagerly in the
// constructor — a kong reflection regression then surfaces as a Describe error at
// plugin registration, loud but never a panic crashing every charly startup (the
// plugin-agent pattern). The served capability carries no #*Input def — a
// command's args are pass-through CLI tokens, not a structured plugin_input — so
// the capability has no InputDef and the plugin ships no CUE schema.
func NewMeta() pb.PluginMetaServer { return agentTeamsMeta{} }

// agentTeamsMeta is the plugin's PluginMetaServer: NewMeta stays trivial (it is
// called at process init by plugins_generated.go) and all fallible reflection
// happens in Describe, which can return an error.
type agentTeamsMeta struct {
	pb.UnimplementedPluginMetaServer
}

func (agentTeamsMeta) Describe(context.Context, *pb.Empty) (*pb.Capabilities, error) {
	model, err := commandModel()
	if err != nil {
		return nil, err
	}
	return sdk.BuildCapabilities(calver,
		[]sdk.ProvidedCapability{
			{Class: "command", Word: "agentteams", CommandModel: model},
		},
		nil, "")
}

type provider struct{ pb.UnimplementedProviderServer }

// Invoke handles OpRun for the COMPILED-IN (in-proc) dispatch: decode the
// pass-through {args} and run the command effect in charly's own process.
// (Out-of-process dispatch is fork/exec → CliMain, never this gRPC path.)
func (provider) Invoke(_ context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	if req.GetClass() != "command" {
		return nil, fmt.Errorf("agentteams: unsupported class %q", req.GetClass())
	}
	if req.GetOp() != sdk.OpRun {
		return nil, fmt.Errorf("agentteams: unsupported op %q (only %q)", req.GetOp(), sdk.OpRun)
	}
	var input struct {
		Args []string `json:"args"`
	}
	if err := json.Unmarshal(req.GetParamsJson(), &input); err != nil {
		return nil, fmt.Errorf("agentteams: decode args: %w", err)
	}
	if err := runCommand(input.Args); err != nil {
		return nil, err
	}
	return &pb.InvokeReply{}, nil
}

// CliMain is the OUT-OF-PROCESS CLI-mode entry (charly fork/execs the binary with
// the pass-through tokens after `charly agentteams`). It runs the SAME effect as
// the in-proc Invoke(OpRun) path.
func CliMain(args []string) int {
	if err := runCommand(args); err != nil {
		fmt.Fprintf(os.Stderr, "charly agentteams: %v\n", err)
		return 1
	}
	return 0
}

// runCommand parses the pass-through args of a COMPILED-IN command — which runs
// in charly's OWN process — so it must NEVER let kong terminate the host: kong's
// default Exit is os.Exit, and a raw kong.New/Parse would make `charly agentteams
// --help` kill charly whole and skip every defer. sdk.RunInProcCLI is the house
// in-proc helper (sdk/clidispatch.go documents the hazard): --help/--version
// print and return nil without running any leaf, a kong-requested non-zero exit
// becomes *sdk.ExitCodeError (honored by the host's exit-code mapping in
// charly/main.go), and a genuine parse error propagates unchanged.
func runCommand(args []string) error {
	var command AgentTeamsCmd
	return sdk.RunInProcCLI("agentteams", &command, args,
		kong.Description("Manage the AgentTeams controller: status, worker/team/human CRUD, declarative apply, and config"))
}

// commandModel reflects the kong grammar into a CLIModel. Every error propagates
// to Describe (no panic): BuildCLIModel fails only on a malformed grammar, and
// that must degrade the plugin's registration, never the host.
func commandModel() (*spec.CLIModel, error) {
	return sdk.BuildCLIModel(&AgentTeamsCmd{}, "agentteams", calver, "agentteams")
}
