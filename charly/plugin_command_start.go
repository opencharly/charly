package main

// startCommand is the `charly start` leaf command extracted into its OWN file as a
// dedicated COMMAND-class provider — the same externalizable dedicated-provider pattern
// (see plugin_command_ssh.go for the full rationale). It self-registers via
// registerDedicatedBuiltin, is absent from builtinProviderInstances + the `providers:`
// manifest, and reaches the CLI root through collectCommandPlugins() → kong.Plugins.
// KongCommand() returns StartCmd verbatim (its Run handler still calls the unchanged core
// machinery), so `charly start …` parses and dispatches exactly as when it was a
// hardcoded CLI field. Only the registration LOCATION moved off the CLI struct.
type startCommand struct{ builtinCommandBase }

func (startCommand) Reserved() string { return "start" }
func (startCommand) KongCommand() any {
	return &struct {
		Start StartCmd `cmd:"" help:"Start a container as a background service"`
	}{}
}

var _ = registerDedicatedBuiltin(startCommand{})
