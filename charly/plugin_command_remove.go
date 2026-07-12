package main

// removeCommand is the `charly remove` leaf command extracted into its OWN file as a
// dedicated COMMAND-class provider — the same externalizable dedicated-provider pattern
// (see plugin_command_ssh.go for the full rationale). It self-registers via
// registerDedicatedBuiltin, is absent from builtinProviderInstances + the `providers:`
// manifest, and reaches the CLI root through collectCommandPlugins() → kong.Plugins.
// KongCommand() returns RemoveCmd verbatim (its Run handler still calls the unchanged
// core machinery), so `charly remove …` parses and dispatches exactly as when it was a
// hardcoded CLI field. Only the registration LOCATION moved off the CLI struct.
type removeCommand struct{ builtinCommandBase }

func (removeCommand) Reserved() string { return "remove" }
func (removeCommand) KongCommand() any {
	return &struct {
		Remove RemoveCmd `cmd:"" help:"Remove service container"`
	}{}
}

var _ = registerDedicatedBuiltin(removeCommand{})
