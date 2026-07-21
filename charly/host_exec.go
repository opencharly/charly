package main

// `charly --host <alias|user@host[:port]> <verb>` — re-exec charly on a
// remote machine over SSH. main() checks for cli.Host != "" before dispatching
// into Kong's ctx.Run() and, if set, calls ReexecOverSSH. Exit code propagates.
//
// S6 (FINAL/K5 unit 6, Cutover-B S3): the mechanism itself — argv rewriting, ssh
// target parsing, and the `ssh` invocation — is 100% stdlib + already-sdk/kit
// (kit.SSHExecutor, kit.EnsureCharlyInDeployVenue, kit.ShellQuote,
// kit.LoadRuntimeConfig), so it relocated wholesale to sdk/kit/reexec_ssh.go. What
// stays HERE is the "should I reexec THIS command path" decision (shouldReexecForHost
// — it must run before Kong dispatches to anything, mirroring
// plugin_command_prescan.go's pre-parse hooks — the SAME "decide-before-dispatch"
// shape core already owns) and resolving the two core-only inputs the kit function
// needs but cannot itself compute: activeCharlyBinary() (the controller's OWN
// resolved path — inspects the running process) and CharlyVersion().

import (
	"fmt"
	"os"
	"strings"

	"github.com/opencharly/sdk/kit"
	"golang.org/x/term"
)

// shouldReexecForHost returns true if charly should forward the current
// invocation to a remote machine via SSH. False when:
//   - cli.Host is empty
//   - the top-level command path starts with one of the LocalOnly
//     commands (settings, version, ssh) — these manage the LOCAL charly
//     installation and must not be re-execed.
//
// `cmdPath` is the space-separated path reported by Kong (e.g.
// "settings get", "check libvirt status").
func shouldReexecForHost(cli *CLI, cmdPath string) bool {
	if cli.Host == "" {
		return false
	}
	head := cmdPath
	if before, _, ok := strings.Cut(cmdPath, " "); ok {
		head = before
	}
	switch head {
	case "settings", "version", "ssh":
		return false
	}
	return true
}

// ReexecOverSSH resolves the core-only inputs (the controller's own resolved binary
// path + version) and delegates the whole mechanism to kit.ReexecOverSSH. See
// sdk/kit/reexec_ssh.go for the argv rewrite / ssh-target-parse / ssh-invoke logic.
func ReexecOverSSH(cli *CLI) int {
	controllerBin, err := activeCharlyBinary()
	if err != nil {
		fmt.Fprintf(os.Stderr, "charly: --host %q: resolve active controller: %v\n", cli.Host, err)
		return 2
	}
	return kit.ReexecOverSSH(kit.ReexecSSHOpts{
		Host:             cli.Host,
		HostIdentityFile: cli.HostIdentityFile,
		HostOption:       cli.HostOption,
	}, os.Args[1:], controllerBin, CharlyVersion(), term.IsTerminal(int(os.Stdin.Fd())))
}
