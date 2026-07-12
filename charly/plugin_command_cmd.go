package main

// cmdCommand is the `charly cmd` leaf command extracted into its OWN file as a
// dedicated COMMAND-class provider — the same externalizable dedicated-provider pattern
// (see plugin_command_ssh.go for the full rationale). It self-registers via
// registerDedicatedBuiltin, is absent from builtinProviderInstances + the `providers:`
// manifest, and reaches the CLI root through collectCommandPlugins() → kong.Plugins.
// KongCommand() returns CmdCmd verbatim (its Run handler still calls the unchanged core
// machinery — resolveContainer, ResolveRuntime, …), so `charly cmd …` parses and
// dispatches exactly as when it was a hardcoded CLI field. Only the registration
// LOCATION moved off the CLI struct; the deploy machinery stays in core.
type cmdCommand struct{ builtinCommandBase }

func (cmdCommand) Reserved() string { return "cmd" }
func (cmdCommand) KongCommand() any {
	return &struct {
		Cmd CmdCmd `cmd:"" help:"Run a command in a running container (with notification)"`
	}{}
}

var _ = registerDedicatedBuiltin(cmdCommand{})
