package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/alecthomas/kong"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/spec"
)

// cli_model_cmd.go implements `charly __cli-model` — the hidden seam that emits charly's
// kong command tree as an sdk.CLIModel JSON document on stdout. It is the host half of the
// CLI-reflection contract: an external COMMAND plugin that must mirror the WHOLE charly CLI
// (the `charly mcp serve` MCP bridge in candy/plugin-mcp) fork/execs this command to learn
// every leaf + its args WITHOUT importing the package-main CLI struct, then drives each
// command by fork/exec'ing `charly <path…> <args>`. Reflecting over the CLI is intrinsic to
// the binary, so this stays in core; the MCP/go-sdk tool surface it feeds lives in the plugin.

// CliModelCmd: `charly __cli-model` (hidden machinery).
type CliModelCmd struct{}

func (CliModelCmd) Run() error {
	model, err := buildCLIModel()
	if err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(model)
}

// buildCLIModel reflects the CLI struct (+ the builtin command-provider grammar, so an
// extracted command like `ssh` is described identically to a hardcoded field) into an
// sdk.CLIModel — the same model walk the MCP server formerly did in-process, now implemented
// ONCE in sdk.BuildCLIModel (R3) so every command plugin and the host emit the same CUE-generated
// contract. EXTERNAL and COMPILED-IN command CANDIES with NO declared subcommand catalog (mcp /
// secrets / udev / vm / alias — dynamic opaque pass-through Args holders) are NOT reflected here —
// they dispatch via syscall.Exec or an in-proc Invoke(OpRun), carrying no per-subcommand shape the
// host could describe. A command-class capability that DOES declare a catalog (F-CLI-NEST — e.g.
// candy/plugin-check's "check" word, candy/plugin-box's "list" word) gets its REAL nested holder
// included here too, so `charly __cli-model` (and therefore MCP tool generation) sees one leaf
// per declared child exactly as if it were a static Kong field.
func buildCLIModel() (*spec.CLIModel, error) {
	var modelCLI CLI
	modelCLI.Plugins = collectCommandPlugins()
	_, _, extCmdTable := collectExternalCommandPlugins()
	declaringTop, declaringNested := declaringCommandHolders(extCmdTable)
	modelCLI.Plugins = append(modelCLI.Plugins, declaringTop...)
	modelCLI.Box.Plugins = append(modelCLI.Box.Plugins, declaringNested["box"]...)
	model, err := sdk.BuildCLIModel(&modelCLI, "charly", CharlyVersion(), "")
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(model.Leaves))
	for _, leaf := range model.Leaves {
		seen[leaf.Path] = true
	}
	for _, provider := range providerRegistry.allProviders() {
		if provider.Class() != ClassCommand {
			continue
		}
		carrier, ok := provider.(interface{ CommandModel() *spec.CLIModel })
		if !ok || carrier.CommandModel() == nil {
			continue
		}
		for _, leaf := range carrier.CommandModel().Leaves {
			if seen[leaf.Path] {
				return nil, fmt.Errorf("duplicate reflected command leaf %q", leaf.Path)
			}
			seen[leaf.Path] = true
			model.Leaves = append(model.Leaves, leaf)
		}
	}
	sort.Slice(model.Leaves, func(i, j int) bool { return model.Leaves[i].Path < model.Leaves[j].Path })
	if err := sdk.ValidateGenerated("#CLIModel", model); err != nil {
		return nil, err
	}
	return model, nil
}

// declaringCommandHolders returns the Kong holders for every command-class capability that
// DECLARED a subcommand catalog (F-CLI-NEST) — bucketed top-level vs CommandParent-nested by its
// dispatch-table key ("check" vs "box list") — so buildCLIModel can fold them into the reflected
// model while PRESERVING the exclusion above for every other (flat, undeclared) command-class
// capability. The per-leaf kong-reflection walk itself now lives in sdk.BuildCLIModel (R3, moved
// out from this file alongside the CommandModel/CUE-generated protocol pipeline) — this helper
// only selects WHICH holders join that walk.
func declaringCommandHolders(table map[string]externalCommandDispatch) (top kong.Plugins, nestedByParent map[string]kong.Plugins) {
	nestedByParent = map[string]kong.Plugins{}
	for key, d := range table {
		if len(d.subcommands) == 0 {
			continue
		}
		if idx := strings.Index(key, " "); idx >= 0 {
			parent := key[:idx]
			nestedByParent[parent] = append(nestedByParent[parent], d.holder)
			continue
		}
		top = append(top, d.holder)
	}
	return top, nestedByParent
}
