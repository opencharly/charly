package main

// statusCommand is the `charly status` leaf command extracted into its OWN file as a
// dedicated COMMAND-class provider — the same externalizable dedicated-provider pattern
// (see plugin_command_ssh.go for the full rationale). It self-registers via
// registerDedicatedBuiltin, is absent from builtinProviderInstances + the `providers:`
// manifest, and reaches the CLI root through collectCommandPlugins() → kong.Plugins.
// KongCommand() returns StatusCmd verbatim (its Run handler still calls the unchanged
// core machinery), so `charly status …` parses and dispatches exactly as when it was a
// hardcoded CLI field. Only the registration LOCATION moved off the CLI struct.
type statusCommand struct{ builtinCommandBase }

func (statusCommand) Reserved() string { return "status" }
func (statusCommand) KongCommand() any {
	return &struct {
		Status StatusCmd `cmd:"" help:"Show service status (all if no box given)"`
	}{}
}

var _ = registerDedicatedBuiltin(statusCommand{})
