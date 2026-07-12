package main

// sshCommand is the `charly ssh` command group extracted into its OWN file as a dedicated
// COMMAND-class provider — the CANONICAL example of the externalizable dedicated-provider
// pattern the leaf-domain + deploy-lifecycle command files share. It self-registers via
// registerDedicatedBuiltin (before any init(), so the registry observes it without a
// cross-init race), is absent from builtinProviderInstances + the `providers:` manifest, and
// reaches the CLI root through collectCommandPlugins() → kong.Plugins. There is no command
// bijection gate (mirroring builders), so the registry resolve IS the wiring proof.
// KongCommand() returns SshCmd verbatim (subcommands + Run handlers unchanged), so
// `charly ssh …` parses and dispatches exactly as when it was a hardcoded CLI field. (A
// command provider may instead be COMPILED-IN as an in-proc command CANDY dispatched via
// Invoke(OpRun) — like candy/plugin-vm's command:vm / candy/plugin-alias's command:alias — or
// EXTERNAL, served out-of-process and syscall.Exec'd like candy/plugin-udev's command:udev;
// this file is the in-charly-module builtin form.)
// `ssh` stays LocalOnly: shouldReexecForHost (host_exec.go) keys off the command-path
// string "ssh", not the CLI struct field, so the --host re-exec exclusion is unaffected.
type sshCommand struct{ builtinCommandBase }

func (sshCommand) Reserved() string { return "ssh" }
func (sshCommand) KongCommand() any {
	return &struct {
		Ssh SshCmd `cmd:"" help:"SSH helpers (tunnel SPICE/VNC/unix sockets from a remote libvirt host to the local machine)"`
	}{}
}

var _ = registerDedicatedBuiltin(sshCommand{})
