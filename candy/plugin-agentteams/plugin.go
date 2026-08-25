// Package agentteams is the importable form of charly's `agentteams` plugin: a
// compiled-in `command:agentteams` management CLI for the AgentTeams controller
// REST API PLUS the `agentteams:` check VERB (the declarative controller-probe
// counterpart, verb.go). A command provider dispatches via the pb Invoke(OpRun)
// envelope — decode the pass-through `{"args":[...]}` and kong-parse them into
// the AgentTeamsCmd tree (sdk.RunInProcCLI), so the handler runs in charly's OWN
// process with native stdio/TTY. The verb provider dispatches via Invoke with the
// full #Op as params_json (the mcp pattern): it resolves the controller's in-venue
// :8090 to a host-routable address over the reverse channel, pulls the admin SA
// token from the venue, and probes with the SAME apiClient the command uses (R3 —
// one REST surface covers the CLI and every bed). Usable COMPILED-IN
// (NewProvider()/NewMeta() via plugins_generated.go) OR served OUT-OF-PROCESS by
// the cmd/serve shim — both placements run the SAME runCommand / runVerbAgentTeams
// (placement-invisible, F8).
package agentteams

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"os"

	"github.com/alecthomas/kong"

	"github.com/opencharly/plugin-agentteams/candy/plugin-agentteams/params"
	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/kit"
	pb "github.com/opencharly/spec/proto"
	"github.com/opencharly/spec/spec"
)

const calver = "2026.224.0600"

//go:embed schema/*.cue
var schemaFS embed.FS

// NewProvider returns the provider for in-proc registration (compiled-in) or
// out-of-proc serving.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta advertises command:agentteams + verb:agentteams via a lazy Describe:
// the kong CLIModel is reflected INSIDE Describe (agentTeamsMeta) rather than
// eagerly in the constructor — a kong reflection regression then surfaces as a
// Describe error at plugin registration, loud but never a panic crashing every
// charly startup (the plugin-agent pattern). The verb capability carries the
// #AgentTeamsInput def served from the plugin's own schema/*.cue (the host
// splices it onto the base and validates every authored `agentteams:` step's
// plugin_input against it); the command capability carries no InputDef — a
// command's args are pass-through CLI tokens, not a structured plugin_input.
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
			{Class: "verb", Word: "agentteams", InputDef: "#AgentTeamsInput", Primary: "method"},
		},
		schemaFS, "schema")
}

type provider struct{ pb.UnimplementedProviderServer }

// Invoke dispatches one operation for the plugin's capabilities. A "command" op
// runs the pass-through CLI args in charly's own process (OpRun); a "verb" op
// runs one `agentteams:` check step (the full #Op as params_json + a CheckEnv
// snapshot as env — the mcp pattern). (Out-of-process command dispatch is
// fork/exec → CliMain, never this gRPC path.)
func (provider) Invoke(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	if req.GetClass() == "command" {
		return invokeCommand(req)
	}
	if req.GetClass() == "verb" {
		return invokeVerb(ctx, req)
	}
	return nil, fmt.Errorf("agentteams: unsupported class %q", req.GetClass())
}

// Reserved implements spec.CheckVerbProvider: the verb word.
func (p *provider) Reserved() string { return "agentteams" }

// RunVerb implements spec.CheckVerbProvider — the COMPILED-IN verb dispatch. The
// host recognizes a compiled-in pb.ProviderServer that ALSO implements this typed
// contract (hostVerbResolver.RunVerb) and threads the live host CheckContext
// (hostCheckContext) in — the executor-bearing surface a host-coupled verb needs
// (ResolveEndpoint + Exec), with NO broker (the mcp pattern's sdk.NewCheckContext
// is out-of-process-only). The out-of-process placement runs the SAME core via
// invokeVerb (the pb Invoke envelope with the broker attached) — placement-
// invisible, F8.
func (p *provider) RunVerb(ctx context.Context, cc spec.CheckContext, op *spec.Op) spec.CheckVerbResult {
	var in params.AgentTeamsInput
	kit.DecodeInput(op.PluginInput, &in)
	method := in.Method
	// Live-deployment verb: skip under `charly check box` (no running controller
	// on a disposable `podman run --rm`) — mirrors the host's box-mode skip.
	if cc.Mode() == spec.CheckModeBox {
		return spec.CheckVerbResult{Status: spec.StatusSkip, Message: fmt.Sprintf("agentteams: %s requires a running controller (skip under charly check box)", method)}
	}
	out, runErr := runVerbAgentTeams(ctx, cc, op, in)
	return verbVerdict(method, out, runErr, op)
}

// verbVerdict grades the verb's output against the authored op matchers
// (exit_status / stdout / stderr) and returns the typed verdict — the SAME shared
// pipeline the out-of-process path runs (sdk.VerbVerdict), converted from the wire
// form to the typed spec.CheckVerbResult a compiled-in RunVerb returns (R3 — one
// verdict pipeline, two return shapes). agentteams produces no artifact.
func verbVerdict(method, out string, runErr error, op *spec.Op) spec.CheckVerbResult {
	reply, err := sdk.VerbVerdict("agentteams", method, out, runErr, op, false)
	if err != nil {
		return spec.CheckVerbResult{Status: spec.StatusFail, Message: err.Error()}
	}
	var wire struct {
		Status  string `json:"status"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(reply.ResultJson, &wire); err != nil {
		return spec.CheckVerbResult{Status: spec.StatusFail, Message: err.Error()}
	}
	status := spec.StatusFail
	switch wire.Status {
	case "pass":
		status = spec.StatusPass
	case "skip":
		status = spec.StatusSkip
	}
	return spec.CheckVerbResult{Status: status, Message: wire.Message}
}

// invokeCommand handles OpRun for the COMPILED-IN (in-proc) dispatch: decode the
// pass-through {args} and run the command effect in charly's own process.
func invokeCommand(req *pb.InvokeRequest) (*pb.InvokeReply, error) {
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

// agentTeamsEnv is the plugin-side decode of the CheckEnv the host ships as
// Operation.Env for an `agentteams:` check step — only Mode/Box matter here (the
// verb probes a live controller, never a container).
type agentTeamsEnv struct {
	Box  string `json:"box"`
	Mode string `json:"mode"` // "live" | "box"
}

// invokeVerb runs one `agentteams:` check operation. It decodes the full #Op, the
// typed plugin input (params.AgentTeamsInput — the method + method-exclusive
// modifiers ride the desugared plugin_input), and the env, skips in box mode (no
// live controller on a disposable `charly check box`), resolves the controller
// over the reverse channel, dispatches the method, and self-evaluates the
// matchers (the out-of-process verb path does NOT run the host-side matcher
// pipeline — this Invoke OWNS the whole verdict, R3).
func invokeVerb(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	var op spec.Op
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &op); err != nil {
			return sdk.ResultJSON("fail", "agentteams: decode op: "+err.Error())
		}
	}
	var in params.AgentTeamsInput
	kit.DecodeInput(op.PluginInput, &in)
	var env agentTeamsEnv
	if len(req.GetEnvJson()) > 0 {
		_ = json.Unmarshal(req.GetEnvJson(), &env)
	}
	method := in.Method

	// Live-deployment verb: skip under `charly check box` (no running controller
	// on a disposable `podman run --rm`) — mirrors the host's box-mode skip.
	if env.Mode == "box" {
		return sdk.ResultJSON("skip", fmt.Sprintf("agentteams: %s requires a running controller (skip under charly check box)", method))
	}
	// Build the reverse-channel check context (ResolveEndpoint + Exec) — dials the
	// broker ONCE; use cc.Exec(), never a second ExecutorForInvoke.
	cc, err := sdk.NewCheckContext(req.GetExecutorBrokerId(), req.GetEnvJson())
	if err != nil {
		return sdk.ResultJSON("fail", fmt.Sprintf("agentteams: %s: %v", method, err))
	}

	out, runErr := runVerbAgentTeams(ctx, cc, &op, in)

	// The shared exit/stdout/stderr verdict pipeline (R3). agentteams produces no
	// artifact.
	return sdk.VerbVerdict("agentteams", method, out, runErr, &op, false)
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
