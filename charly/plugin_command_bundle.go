package main

// bundleCommand is the `charly bundle` command group extracted into its OWN file as a
// dedicated COMMAND-class provider — the same externalizable dedicated-provider pattern
// (see plugin_command_alias.go for the full rationale). It self-registers via
// registerDedicatedBuiltin, is absent from builtinProviderInstances + the `providers:`
// manifest, and reaches the CLI root through collectCommandPlugins() → kong.Plugins.
// KongCommand() returns BundleCmd verbatim (subcommands + Run handlers unchanged, all
// still calling the unchanged core deploy machinery — LoadBundleConfig, the deploy
// targets, …), so `charly bundle …` parses and dispatches exactly as when it was a
// hardcoded CLI field. Only the registration LOCATION moved off the CLI struct.
type bundleCommand struct{ builtinCommandBase }

func (bundleCommand) Reserved() string { return "bundle" }
func (bundleCommand) KongCommand() any {
	return &struct {
		Bundle BundleCmd `cmd:"" help:"Manage charly.yml bundle (deployment) overrides"`
	}{}
}

var _ = registerDedicatedBuiltin(bundleCommand{})
