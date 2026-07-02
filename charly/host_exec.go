package main

// `charly --host <alias|user@host[:port]> <verb>` — re-exec charly on a
// remote machine over SSH. Shells out to the system `ssh` binary so
// ~/.ssh/config, agent forwarding, and ControlMaster all Just Work.
//
// main() checks for cli.Host != "" before dispatching into Kong's
// ctx.Run() and, if set, rewrites the argv to drop --host / --dir /
// --repo (those are client-side concerns) and re-execs on the remote
// host. Exit code propagates.

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

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

// ReexecOverSSH rewrites os.Args by stripping --host and the client-
// local path flags (--dir/-C, --repo), then invokes
// `ssh <resolved-target> charly <rest of argv>`. Stdin/stdout/stderr are
// piped straight through. The returned exit code is whatever `ssh`
// exits with (which propagates the remote `charly` exit code).
func ReexecOverSSH(cli *CLI) int {
	target, err := resolveHostAlias(cli.Host)
	if err != nil {
		fmt.Fprintf(os.Stderr, "charly: --host %q: %v\n", cli.Host, err)
		return 2
	}
	remoteArgv := buildRemoteArgv(os.Args[1:])
	cmd := exec.Command("ssh", sshCmdArgs(target, remoteArgv)...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if ee, ok := errors.AsType[*exec.ExitError](err); ok {
			return ee.ExitCode()
		}
		fmt.Fprintf(os.Stderr, "charly: ssh %s: %v\n", target, err)
		return 1
	}
	return 0
}

// resolveHostAlias looks up the `hosts.<alias>` setting if the input
// doesn't already look like an ssh target (user@host or host[:port]).
// Returns the raw string when no alias match exists — matches the
// behavior of `git remote add` / `kubectl --context`.
func resolveHostAlias(h string) (string, error) {
	if h == "" {
		return "", fmt.Errorf("empty host")
	}
	// Looks like an ssh target already? (contains @ or a dot)
	if strings.ContainsAny(h, "@.") {
		return h, nil
	}
	// Try alias lookup.
	cfg, err := LoadRuntimeConfig()
	if err != nil {
		// Fall back to raw — let ssh resolve via ~/.ssh/config.
		return h, nil
	}
	if v, ok := cfg.HostAliases[h]; ok && v != "" {
		return v, nil
	}
	// Not a configured alias — pass through and let ssh try its own
	// host resolution (~/.ssh/config Host entries, DNS, etc.).
	return h, nil
}

// buildRemoteArgv strips client-only flags from argv before shipping
// it to the remote host.
//
// Stripped:
//   - --host X  /  --host=X
//   - --dir / -C X  /  --dir=X
//   - --repo X / --repo=X
//
// Everything else is passed through verbatim.
func buildRemoteArgv(argv []string) []string {
	out := make([]string, 0, len(argv))
	skipNext := false
	for i := range argv {
		a := argv[i]
		if skipNext {
			skipNext = false
			continue
		}
		if a == "--host" || a == "--dir" || a == "-C" || a == "--repo" {
			skipNext = true
			continue
		}
		if strings.HasPrefix(a, "--host=") ||
			strings.HasPrefix(a, "--dir=") ||
			strings.HasPrefix(a, "-C=") ||
			strings.HasPrefix(a, "--repo=") {
			continue
		}
		_ = i
		out = append(out, a)
	}
	return out
}

// sshCmdArgs builds the full argv for the `ssh` process:
//
//	ssh [-tt] <target> charly <remoteArgv...>
//
// -tt allocates a pseudo-TTY when stdin is a TTY, so interactive
// programs (prompts, pagers) work; piped stdin gets plain mode.
func sshCmdArgs(target string, remoteArgv []string) []string {
	args := make([]string, 0, 4+len(remoteArgv))
	if term.IsTerminal(int(os.Stdin.Fd())) {
		args = append(args, "-tt")
	}
	args = append(args, target, "charly")
	args = append(args, remoteArgv...)
	return args
}
