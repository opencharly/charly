package main

// stopCommand is the `charly stop` leaf command extracted into its OWN file as a
// dedicated COMMAND-class provider — the same externalizable dedicated-provider pattern
// (see plugin_command_alias.go for the full rationale). It self-registers via
// registerDedicatedBuiltin, is absent from builtinProviderInstances + the `providers:`
// manifest, and reaches the CLI root through collectCommandPlugins() → kong.Plugins.
// KongCommand() returns StopCmd verbatim (its Run handler still calls the unchanged core
// machinery), so `charly stop …` parses and dispatches exactly as when it was a
// hardcoded CLI field. Only the registration LOCATION moved off the CLI struct.
type stopCommand struct{ builtinCommandBase }

func (stopCommand) Reserved() string { return "stop" }
func (stopCommand) KongCommand() any {
	return &struct {
		Stop StopCmd `cmd:"" help:"Stop a running service container"`
	}{}
}

var _ = registerDedicatedBuiltin(stopCommand{})
