package main

// cpCommand is the `charly cp` leaf command extracted into its OWN file as a
// dedicated COMMAND-class provider — the same externalizable dedicated-provider pattern
// (see plugin_command_ssh.go for the full rationale). It self-registers via
// registerDedicatedBuiltin, is absent from builtinProviderInstances + the `providers:`
// manifest, and reaches the CLI root through collectCommandPlugins() → kong.Plugins.
// KongCommand() returns CpCmd verbatim (its Run handler still calls the unchanged core
// machinery), so `charly cp …` parses and dispatches exactly as when it was a hardcoded
// CLI field. Only the registration LOCATION moved off the CLI struct.
type cpCommand struct{ builtinCommandBase }

func (cpCommand) Reserved() string { return "cp" }
func (cpCommand) KongCommand() any {
	return &struct {
		Cp CpCmd `cmd:"" name:"cp" help:"Copy a file between the host and a running container (':' prefix marks the container side)"`
	}{}
}

var _ = registerDedicatedBuiltin(cpCommand{})
