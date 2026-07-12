package main

import "github.com/alecthomas/kong"

// checkCommandHolder is the Kong grammar holder for the extracted `charly check`
// command tree. Unlike the other dedicated command providers (which return an
// ANONYMOUS holder struct from KongCommand), check needs a NAMED holder for ONE
// reason: the out-of-process NESTED command plugins — `charly check kube/adb`,
// each a NestedCommandProvider with CommandParent()=="check" — must be injected
// into CheckCmd's embedded kong.Plugins AFTER collectExternalCommandPlugins() runs
// in main(). A named type lets main() find this holder in cli.Plugins by a single
// type assertion (attachNestedCheckPlugins) and set Check.Plugins — the exact wiring
// that previously read `cli.Check.Plugins = nestedCmds["check"]` when check was a
// hardcoded CLI field. The same holder pointers built by collectExternalCommandPlugins()
// flow through unchanged, so the post-parse dispatch table still reads the kong-filled
// args out of them.
type checkCommandHolder struct {
	Check CheckCmd `cmd:"" help:"Evaluate boxes and deployments — pure-box (disposable), live (running deployment), AI-driven iteration, and live-container probe verbs (cdp/wl/dbus/vnc/mcp/spice/libvirt/record/k8s)"`
}

// checkCommand is the `charly check` command tree extracted into its OWN file as a
// dedicated COMMAND-class provider — the same externalizable dedicated-provider pattern
// (see plugin_command_ssh.go for the full rationale). It self-registers via
// registerDedicatedBuiltin, is absent from builtinProviderInstances + the `providers:`
// manifest, and reaches the CLI root through collectCommandPlugins() → kong.Plugins.
// KongCommand() returns a fresh checkCommandHolder (CheckCmd verbatim, with an EMPTY
// nested-plugin embed), so `charly check …` parses and dispatches through CheckCmd's
// compiled-in subcommands + Run handlers exactly as when it was a hardcoded CLI field.
// The nested external check subcommands are attached afterwards in main() via
// attachNestedCheckPlugins; the MCP-server build sites leave them empty (matching the
// prior behaviour, where the MCP modelCLI never set cli.Check.Plugins).
type checkCommand struct{ builtinCommandBase }

func (checkCommand) Reserved() string { return "check" }
func (checkCommand) KongCommand() any { return &checkCommandHolder{} }

var _ = registerDedicatedBuiltin(checkCommand{})

// attachNestedCheckPlugins injects the nested external command plugins (the
// NestedCommandProvider set whose CommandParent()=="check": `charly check
// kube/adb`) into the extracted check command's CheckCmd.Plugins embed. It
// finds the *checkCommandHolder that collectCommandPlugins() placed into the CLI
// grammar and mutates it in place; nested is the collectExternalCommandPlugins()
// nestedByParent["check"] slice. A no-op when nested is empty or the holder is
// absent (e.g. an MCP modelCLI that never collected the check command), preserving
// the pre-extraction wiring where only main() attached the nested check subcommands.
func attachNestedCheckPlugins(plugins kong.Plugins, nested kong.Plugins) {
	if len(nested) == 0 {
		return
	}
	for _, p := range plugins {
		if h, ok := p.(*checkCommandHolder); ok {
			h.Check.Plugins = nested
			return
		}
	}
}
