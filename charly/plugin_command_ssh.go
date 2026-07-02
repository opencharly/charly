package main

// sshCommand is the `charly ssh` command group as a dedicated COMMAND-class provider
// (the externalizable dedicated-provider pattern — see plugin_command_alias.go). It
// self-registers via registerDedicatedBuiltin and reaches the CLI root through
// collectCommandPlugins() → kong.Plugins; KongCommand() returns SshCmd verbatim, so
// `charly ssh …` parses and dispatches exactly as when it was a hardcoded CLI field.
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
