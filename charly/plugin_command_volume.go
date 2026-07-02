package main

// volumeCommand is the `charly volume` command group extracted into its OWN file as a
// dedicated COMMAND-class provider — the same externalizable dedicated-provider pattern
// (see plugin_command_alias.go for the full rationale). It self-registers via
// registerDedicatedBuiltin, is absent from builtinProviderInstances + the `providers:`
// manifest, and reaches the CLI root through collectCommandPlugins() → kong.Plugins.
// KongCommand() returns VolumeCmd verbatim (subcommands + Run handlers unchanged), so
// `charly volume …` parses and dispatches exactly as when it was a hardcoded CLI field.
// Only the registration LOCATION moved off the CLI struct.
type volumeCommand struct{ builtinCommandBase }

func (volumeCommand) Reserved() string { return "volume" }
func (volumeCommand) KongCommand() any {
	return &struct {
		Volume VolumeCmd `cmd:"" name:"volume" help:"List or reset a deployment's charly-managed named volumes"`
	}{}
}

var _ = registerDedicatedBuiltin(volumeCommand{})
