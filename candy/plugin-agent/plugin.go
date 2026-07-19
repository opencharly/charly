// Package agentkind is the importable form of charly's `agent` plugin KIND. A KIND provider
// dispatches via the pb Invoke(OpLoad) envelope — decode the authored `agent:` entity into
// the core spec.Agent and re-marshal as canonical JSON; the host lands it in
// uf.PluginKinds["agent"][<name>]. Usable COMPILED-IN (NewProvider()/NewMeta() via
// plugins_generated.go) OR served OUT-OF-PROCESS by the cmd/serve shim. Relocated out of
// charly's module (formerly charly/plugin/builtins/agent + charly/plugin_agent.go).
package agentkind

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/alecthomas/kong"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/agentkit"
	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk/spec"
)

//go:embed schema/*.cue
var schemaFS embed.FS

var agentSchema struct {
	sync.Once
	validator *sdk.SchemaValidator
	err       error
}

func validateAgentJSON(definition string, payload []byte) error {
	agentSchema.Do(func() {
		agentSchema.validator, agentSchema.err = sdk.NewSchemaValidator(schemaFS, "schema")
	})
	if agentSchema.err != nil {
		return fmt.Errorf("agent kind: %w", agentSchema.err)
	}
	if err := agentSchema.validator.ValidateJSON(definition, payload); err != nil {
		return fmt.Errorf("agent kind: %w", err)
	}
	return nil
}

// NewProvider returns the kind provider for in-proc registration or out-of-proc serving.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta advertises the kind's capability (Class "kind", word "agent") + its
// self-contained CUE schema. The command CLIModels are reflected LAZILY inside
// Describe (agentMeta) rather than eagerly in the constructor: a kong
// reflection regression then surfaces as a Describe error at plugin
// registration — loud, but never a panic crashing every charly startup.
func NewMeta() pb.PluginMetaServer { return agentMeta{} }

// agentMeta is the plugin's PluginMetaServer: NewMeta stays trivial (it is
// called at process init by plugins_generated.go) and all fallible reflection
// happens in Describe, which can return an error.
type agentMeta struct {
	pb.UnimplementedPluginMetaServer
}

func (agentMeta) Describe(context.Context, *pb.Empty) (*pb.Capabilities, error) {
	agentModel, tuiModel, tmuxModel, err := commandModels()
	if err != nil {
		return nil, err
	}
	return sdk.BuildCapabilities("2026.199.1330",
		[]sdk.ProvidedCapability{
			{Class: "kind", Word: "agent", InputDef: "#AgentInput"},
			{Class: "kind", Word: "agent-team", InputDef: "#AgentTeamInput"},
			{Class: "command", Word: "agent", CommandModel: agentModel},
			{Class: "command", Word: "tui", CommandModel: tuiModel},
			{Class: "command", Word: "tmux", CommandModel: tmuxModel},
		},
		schemaFS, "schema")
}

type provider struct{ pb.UnimplementedProviderServer }

// Invoke handles two ops:
//   - OpLoad: decode the authored `agent:` entity into spec.Agent and return it
//     re-marshalled as canonical JSON (the host validated it against #AgentInput).
//   - OpResolve: the agent de-type (Cutover E) — the host hands the opaque agent
//     catalog + a selected name; this plugin applies name-selection + defaults and
//     returns a generic AgentExecSpec the kernel's harness runs (resolve.go).
func (provider) Invoke(_ context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	if req.GetClass() == "command" {
		if req.GetOp() != sdk.OpRun {
			return nil, fmt.Errorf("agent control command: unsupported op %q", req.GetOp())
		}
		var input struct {
			Args []string `json:"args"`
		}
		if err := json.Unmarshal(req.GetParamsJson(), &input); err != nil {
			return nil, fmt.Errorf("agent control command: decode args: %w", err)
		}
		if err := runCommand(req.GetReserved(), input.Args); err != nil {
			return nil, err
		}
		return &pb.InvokeReply{}, nil
	}
	switch req.GetOp() {
	case sdk.OpLoad:
		var in any
		definition := ""
		switch req.GetReserved() {
		case "agent":
			in = &spec.Agent{}
			definition = "#AgentInput"
		case "agent-team":
			in = &spec.AgentTeam{}
			definition = "#AgentTeamInput"
		default:
			return nil, fmt.Errorf("agent kind: unsupported word %q", req.GetReserved())
		}
		if len(req.GetParamsJson()) == 0 {
			return nil, errors.New("agent kind: load requires a CUE input payload")
		}
		if err := validateAgentJSON(definition, req.GetParamsJson()); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(req.GetParamsJson(), in); err != nil {
			return nil, fmt.Errorf("agent kind: decode entity: %w", err)
		}
		switch value := in.(type) {
		case *spec.Agent:
			// The raw plugin-owned CUE validation above preserves omitted CUE
			// defaults. Encoding the Go zero value first would turn an omitted
			// prompt_via default into an explicit invalid empty string.
		case *spec.AgentTeam:
			if err := sdk.ValidateGenerated("#AgentTeam", value); err != nil {
				return nil, fmt.Errorf("agent-team kind: %w", err)
			}
			if err := agentkit.ValidateTeam(*value); err != nil {
				return nil, err
			}
		}
		out, err := json.Marshal(in)
		if err != nil {
			return nil, fmt.Errorf("agent kind: marshal entity: %w", err)
		}
		return &pb.InvokeReply{ResultJson: out}, nil
	case sdk.OpResolve:
		var in spec.AgentResolveInput
		if len(req.GetParamsJson()) > 0 {
			if err := json.Unmarshal(req.GetParamsJson(), &in); err != nil {
				return nil, fmt.Errorf("agent resolve: decode input: %w", err)
			}
		}
		reply, err := resolveAgent(in)
		if err != nil {
			return nil, err
		}
		out, err := json.Marshal(reply)
		if err != nil {
			return nil, fmt.Errorf("agent resolve: marshal reply: %w", err)
		}
		return &pb.InvokeReply{ResultJson: out}, nil
	default:
		return nil, fmt.Errorf("agent kind: unsupported op %q", req.GetOp())
	}
}

// CliMain is the out-of-process placement for command:agent and command:tui.
// Core stamps CHARLY_COMMAND_WORD before exec; a direct development invocation
// defaults to the primary agent command.
func CliMain(args []string) int {
	word := os.Getenv("CHARLY_COMMAND_WORD")
	if word == "" {
		word = "agent"
	}
	if err := runCommand(word, args); err != nil {
		fmt.Fprintf(os.Stderr, "charly %s: %v\n", word, err)
		return 1
	}
	return 0
}

// runCommand parses the pass-through args of a COMPILED-IN command — which runs
// in charly's OWN process — so it must NEVER let kong terminate the host: kong's
// default Exit is os.Exit, and a raw kong.New/Parse would make `charly agent
// --help` kill charly whole and skip every defer. sdk.RunInProcCLI is the house
// in-proc helper (sdk/clidispatch.go documents the hazard): --help/--version
// print and return nil without running any leaf, a kong-requested non-zero exit
// becomes *sdk.ExitCodeError (honored by the host's exit-code mapping in
// charly/main.go), and a genuine parse error propagates unchanged.
func runCommand(word string, args []string) error {
	switch word {
	case "agent":
		var command AgentCmd
		return sdk.RunInProcCLI("agent", &command, args,
			kong.Description("Durable, transport-neutral agent sessions, runs, terminals, teams, and recovery"))
	case "tui":
		var command TuiCmd
		return sdk.RunInProcCLI("tui", &command, args,
			kong.Description("Open the thin Pi-TUI client for the typed agent control plane"))
	case "tmux":
		var command TmuxCompatCmd
		return sdk.RunInProcCLI("tmux", &command, args,
			kong.Description("Compatibility facade over typed terminal:tmux channels"))
	default:
		return fmt.Errorf("unsupported command word %q", word)
	}
}

// commandModels reflects the three kong grammars into CLIModels. Every error
// propagates to Describe (no panic): BuildCLIModel fails only on a malformed
// grammar, and that must degrade the plugin's registration, never the host.
func commandModels() (agentModel, tuiModel, tmuxModel *spec.CLIModel, err error) {
	agentModel, err = sdk.BuildCLIModel(&AgentCmd{}, "agent", "2026.199.1330", "agent")
	if err != nil {
		return nil, nil, nil, err
	}
	type tuiRoot struct {
		Tui TuiCmd `cmd:"" name:"tui" help:"Open the thin Pi-TUI client for the typed agent control plane"`
	}
	tuiModel, err = sdk.BuildCLIModel(&tuiRoot{}, "charly", "2026.199.1330", "")
	if err != nil {
		return nil, nil, nil, err
	}
	tmuxModel, err = sdk.BuildCLIModel(&TmuxCompatCmd{}, "tmux", "2026.199.1330", "tmux")
	if err != nil {
		return nil, nil, nil, err
	}
	return agentModel, tuiModel, tmuxModel, nil
}
