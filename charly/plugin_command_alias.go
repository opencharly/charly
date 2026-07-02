package main

// aliasCommand is the `charly alias` command group extracted into its OWN file as a
// dedicated COMMAND-class provider — the CANONICAL example of the externalizable
// dedicated-provider pattern the leaf-domain + deploy-lifecycle command files share. It
// self-registers via registerDedicatedBuiltin (before any init(), so the registry
// observes it without a cross-init race), is absent from builtinProviderInstances + the
// `providers:` manifest, and reaches the CLI root through collectCommandPlugins() →
// kong.Plugins. There is no command bijection gate (mirroring builders), so the registry
// resolve IS the wiring proof. KongCommand() returns AliasCmd verbatim (subcommands + Run
// handlers unchanged), so `charly alias …` parses and dispatches exactly as when it was a
// hardcoded CLI field. (A command provider may instead be EXTERNAL — served
// out-of-process and syscall.Exec'd, like candy/plugin-udev's command:udev; this file is
// the in-process builtin form.)
type aliasCommand struct{ builtinCommandBase }

func (aliasCommand) Reserved() string { return "alias" }
func (aliasCommand) KongCommand() any {
	return &struct {
		Alias AliasCmd `cmd:"" help:"Manage command aliases for container images"`
	}{}
}

var _ = registerDedicatedBuiltin(aliasCommand{})
