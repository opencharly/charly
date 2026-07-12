package main

// reapOrphansCommand is the `charly reap-orphans` leaf command extracted into its OWN
// file as a dedicated COMMAND-class provider — the same externalizable dedicated-provider
// pattern (see plugin_command_ssh.go for the full rationale). It self-registers via
// registerDedicatedBuiltin, is absent from builtinProviderInstances + the `providers:`
// manifest, and reaches the CLI root through collectCommandPlugins() → kong.Plugins.
// KongCommand() returns ReapOrphansCmd verbatim (its Run handler still calls the
// unchanged core machinery — LoadBundleConfig + shelling to `charly bundle del`), so
// `charly reap-orphans` parses and dispatches exactly as when it was a hardcoded CLI
// field. Only the registration LOCATION moved off the CLI struct. The filename uses an
// underscore (reap_orphans) while Reserved() returns the hyphenated CLI word.
type reapOrphansCommand struct{ builtinCommandBase }

func (reapOrphansCommand) Reserved() string { return "reap-orphans" }
func (reapOrphansCommand) KongCommand() any {
	return &struct {
		ReapOrphans ReapOrphansCmd `cmd:"reap-orphans" help:"Find ephemeral deployments whose underlying resource is gone and clean them up"`
	}{}
}

var _ = registerDedicatedBuiltin(reapOrphansCommand{})
