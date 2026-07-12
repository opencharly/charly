package main

// restartCommand is the `charly restart` leaf command extracted into its OWN file as a
// dedicated COMMAND-class provider — the same externalizable dedicated-provider pattern
// (see plugin_command_ssh.go for the full rationale). It self-registers via
// registerDedicatedBuiltin, is absent from builtinProviderInstances + the `providers:`
// manifest, and reaches the CLI root through collectCommandPlugins() → kong.Plugins.
// KongCommand() returns RestartCmd verbatim (its Run handler still calls the unchanged
// core machinery), so `charly restart …` parses and dispatches exactly as when it was a
// hardcoded CLI field. Only the registration LOCATION moved off the CLI struct.
type restartCommand struct{ builtinCommandBase }

func (restartCommand) Reserved() string { return "restart" }
func (restartCommand) KongCommand() any {
	return &struct {
		Restart RestartCmd `cmd:"" help:"Restart a service container atomically (systemctl --user restart)"`
	}{}
}

var _ = registerDedicatedBuiltin(restartCommand{})
