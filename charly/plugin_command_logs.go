package main

// logsCommand is the `charly logs` leaf command extracted into its OWN file as a
// dedicated COMMAND-class provider — the same externalizable dedicated-provider pattern
// (see plugin_command_ssh.go for the full rationale). It self-registers via
// registerDedicatedBuiltin, is absent from builtinProviderInstances + the `providers:`
// manifest, and reaches the CLI root through collectCommandPlugins() → kong.Plugins.
// KongCommand() returns LogsCmd verbatim (its Run handler still calls the unchanged core
// machinery), so `charly logs …` parses and dispatches exactly as when it was a
// hardcoded CLI field. Only the registration LOCATION moved off the CLI struct.
type logsCommand struct{ builtinCommandBase }

func (logsCommand) Reserved() string { return "logs" }
func (logsCommand) KongCommand() any {
	return &struct {
		Logs LogsCmd `cmd:"" help:"Show service container logs"`
	}{}
}

var _ = registerDedicatedBuiltin(logsCommand{})
