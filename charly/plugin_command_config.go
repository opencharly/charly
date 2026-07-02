package main

// configCommand is the `charly config` command group extracted into its OWN file as a
// dedicated COMMAND-class provider — the same externalizable dedicated-provider pattern
// (see plugin_command_alias.go for the full rationale). It self-registers via
// registerDedicatedBuiltin, is absent from builtinProviderInstances + the `providers:`
// manifest, and reaches the CLI root through collectCommandPlugins() → kong.Plugins.
// KongCommand() returns BoxConfigCmd verbatim (subcommands + Run handlers unchanged, all
// still calling the unchanged core deploy machinery), so `charly config …` parses and
// dispatches exactly as when it was a hardcoded CLI field. Only the registration
// LOCATION moved off the CLI struct; the deploy machinery stays in core.
type configCommand struct{ builtinCommandBase }

func (configCommand) Reserved() string { return "config" }
func (configCommand) KongCommand() any {
	return &struct {
		Config BoxConfigCmd `cmd:"" help:"Configure box deployment (setup, secrets, encrypted volumes)"`
	}{}
}

var _ = registerDedicatedBuiltin(configCommand{})
