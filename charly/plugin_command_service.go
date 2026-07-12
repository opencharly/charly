package main

// serviceCommand is the `charly service` command group extracted into its OWN file as a
// dedicated COMMAND-class provider — the same externalizable dedicated-provider pattern
// (see plugin_command_ssh.go for the full rationale). It self-registers via
// registerDedicatedBuiltin, is absent from builtinProviderInstances + the `providers:`
// manifest, and reaches the CLI root through collectCommandPlugins() → kong.Plugins.
// KongCommand() returns ServiceCmd verbatim (subcommands + Run handlers unchanged), so
// `charly service …` parses and dispatches exactly as when it was a hardcoded CLI field.
// Only the registration LOCATION moved off the CLI struct.
type serviceCommand struct{ builtinCommandBase }

func (serviceCommand) Reserved() string { return "service" }
func (serviceCommand) KongCommand() any {
	return &struct {
		Service ServiceCmd `cmd:"" help:"Manage supervisord services inside a running container"`
	}{}
}

var _ = registerDedicatedBuiltin(serviceCommand{})
