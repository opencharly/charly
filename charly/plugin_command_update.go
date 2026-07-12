package main

// updateCommand is the `charly update` leaf command extracted into its OWN file as a
// dedicated COMMAND-class provider — the same externalizable dedicated-provider pattern
// (see plugin_command_ssh.go for the full rationale). It self-registers via
// registerDedicatedBuiltin, is absent from builtinProviderInstances + the `providers:`
// manifest, and reaches the CLI root through collectCommandPlugins() → kong.Plugins.
// KongCommand() returns UpdateCmd verbatim (its Run handler still calls the unchanged
// core machinery — the per-target update dispatch), so `charly update …` parses and
// dispatches exactly as when it was a hardcoded CLI field. Only the registration
// LOCATION moved off the CLI struct.
type updateCommand struct{ builtinCommandBase }

func (updateCommand) Reserved() string { return "update" }
func (updateCommand) KongCommand() any {
	return &struct {
		Update UpdateCmd `cmd:"" help:"Update box and restart if active"`
	}{}
}

var _ = registerDedicatedBuiltin(updateCommand{})
