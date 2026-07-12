package main

// shellCommand is the `charly shell` leaf command extracted into its OWN file as a
// dedicated COMMAND-class provider — the same externalizable dedicated-provider pattern
// (see plugin_command_ssh.go for the full rationale). It self-registers via
// registerDedicatedBuiltin, is absent from builtinProviderInstances + the `providers:`
// manifest, and reaches the CLI root through collectCommandPlugins() → kong.Plugins.
// KongCommand() returns ShellCmd verbatim (its Run handler still calls the unchanged core
// machinery), so `charly shell …` parses and dispatches exactly as when it was a
// hardcoded CLI field. Only the registration LOCATION moved off the CLI struct.
type shellCommand struct{ builtinCommandBase }

func (shellCommand) Reserved() string { return "shell" }
func (shellCommand) KongCommand() any {
	return &struct {
		Shell ShellCmd `cmd:"" help:"Start a bash shell in a container image"`
	}{}
}

var _ = registerDedicatedBuiltin(shellCommand{})
