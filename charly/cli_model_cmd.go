package main

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"

	"github.com/alecthomas/kong"

	"github.com/opencharly/sdk"
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
// sdk.CLIModel — the same model walk the MCP server formerly did in-process. EXTERNAL and
// COMPILED-IN command CANDIES (mcp / secrets / udev / vm / alias, dynamic pass-through Args
// holders) are NOT reflected here — they dispatch via syscall.Exec or an in-proc Invoke(OpRun),
// not the reflected builtin-CommandProvider grammar, so they carry no per-subcommand model.
func buildCLIModel() (*sdk.CLIModel, error) {
	var modelCLI CLI
	modelCLI.Plugins = collectCommandPlugins()
	k, err := kong.New(&modelCLI, kong.Name("charly"), kong.UsageOnError())
	if err != nil {
		return nil, fmt.Errorf("building kong model: %w", err)
	}
	model := &sdk.CLIModel{Name: "charly", Version: CharlyVersion()}
	for _, leaf := range k.Model.Leaves(true) {
		model.Leaves = append(model.Leaves, kongLeafToModelLeaf(leaf))
	}
	return model, nil
}

// kongLeafToModelLeaf converts one Kong leaf into the sdk.CLILeaf wire shape.
func kongLeafToModelLeaf(leaf *kong.Node) sdk.CLILeaf {
	out := sdk.CLILeaf{
		Path:   leafPath(leaf),
		Help:   strings.TrimSpace(leaf.Help),
		Hidden: false,
	}
	// Hidden machinery (the `__`-prefixed leaves like __plugin / __cli-model) is marked
	// so a consumer skips it without a hand-maintained list.
	if strings.HasPrefix(out.Path, "__") {
		out.Hidden = true
	}
	for _, pos := range leaf.Positional {
		out.Positionals = append(out.Positionals, kongValueToArg(pos, "", pos.Required))
	}
	seen := map[string]bool{}
	for _, pos := range leaf.Positional {
		seen[sanitizePropName(pos.Name)] = true
	}
	for _, group := range leaf.AllFlags(true) {
		for _, f := range group {
			if f.Hidden || isHelpFlag(f) {
				continue
			}
			prop := sanitizePropName(f.Name)
			if seen[prop] {
				continue
			}
			seen[prop] = true
			arg := kongValueToArg(f.Value, f.Name, f.Required)
			arg.IsBool = f.IsBool()
			arg.Negated = f.Negated
			out.Flags = append(out.Flags, arg)
		}
	}
	return out
}

// kongValueToArg infers the JSON-schema-relevant facts of a positional or flag.
func kongValueToArg(v *kong.Value, flagName string, required bool) sdk.CLIArg {
	arg := sdk.CLIArg{Prop: sanitizePropName(v.Name), Name: flagName, Help: v.Help, Required: required}
	// Gate on v.Enum (kong's EnumSlice() returns [""] for a non-enum value, which would
	// otherwise emit a spurious single-empty-string enum on every arg).
	if v.Enum != "" {
		for _, e := range v.EnumSlice() {
			arg.Enum = append(arg.Enum, fmt.Sprint(e))
		}
	}
	if v.IsSlice() {
		arg.Type = "array"
		arg.IsSlice = true
		arg.ElemType = jsonTypeForKind(v.Target.Type().Elem().Kind())
		return arg
	}
	if v.IsMap() {
		arg.Type = "object"
		arg.IsMap = true
		return arg
	}
	arg.Type = jsonTypeForKind(v.Target.Kind())
	if v.HasDefault && v.Default != "" {
		arg.HasDefault = true
		arg.Default = v.Default
	}
	return arg
}

// leafPath returns a dotted command path like "box.build" from Kong's space-separated Path().
func leafPath(n *kong.Node) string { return strings.ReplaceAll(n.Path(), " ", ".") }

func isHelpFlag(f *kong.Flag) bool { return f.Name == "help" || f.Name == "help-all" }

// sanitizePropName lowercases and replaces "-" with "_" so JSON-schema properties are
// idiomatic snake_case; the consumer reverses it via the model's CLIArg.Name.
func sanitizePropName(s string) string { return strings.ReplaceAll(strings.ToLower(s), "-", "_") }

// jsonTypeForKind maps a reflect.Kind to a JSON-schema primitive type (durations / addrs
// round-trip as strings).
func jsonTypeForKind(k reflect.Kind) string {
	switch k {
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "integer"
	case reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Slice, reflect.Array:
		return "array"
	case reflect.Map, reflect.Struct:
		return "object"
	default:
		return "string"
	}
}
