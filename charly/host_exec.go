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
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
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

// ReexecOverSSH rewrites os.Args by stripping --host and the client-
// local path flags (--dir/-C, --repo), resolves the remote charly
// endpoint (the venue's own PATH charly when it is at least as new as
// the local controller; otherwise a version-gated replica of the local
// binary delivered by kit.EnsureCharlyInDeployVenue), then invokes
// `ssh <resolved-target> <endpoint> <rest of argv>`. Stdin/stdout/stderr
// are piped straight through. The returned exit code is whatever `ssh`
// exits with (which propagates the remote `charly` exit code). The
// happy path prints nothing — a diagnostic appears only when the local
// binary is actually replicated or the bootstrap fails.
func ReexecOverSSH(cli *CLI) int {
	target, err := resolveHostAlias(cli.Host)
	if err != nil {
		fmt.Fprintf(os.Stderr, "charly: --host %q: %v\n", cli.Host, err)
		return 2
	}
	remoteArgv := buildRemoteArgv(os.Args[1:])
	destination, portText, err := splitSSHTarget(target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "charly: --host %q: %v\n", cli.Host, err)
		return 2
	}
	user, host := "", destination
	if at := strings.LastIndex(destination, "@"); at >= 0 {
		user, host = destination[:at], destination[at+1:]
	}
	port := 0
	if portText != "" {
		port, _ = strconv.Atoi(portText) // splitSSHTarget already validated it.
	}
	extra := make([]string, 0, 2+len(cli.HostOption)*2)
	if cli.HostIdentityFile != "" {
		extra = append(extra, "-i", cli.HostIdentityFile)
	}
	for _, option := range cli.HostOption {
		extra = append(extra, "-o", option)
	}
	executor := &kit.SSHExecutor{User: user, Host: host, Port: port, Args: extra}
	controllerBin, err := activeCharlyBinary()
	if err != nil {
		fmt.Fprintf(os.Stderr, "charly: --host %q: resolve active controller: %v\n", cli.Host, err)
		return 2
	}
	remoteBin, err := kit.EnsureCharlyInDeployVenue(context.Background(), executor, controllerBin, CharlyVersion())
	if err != nil {
		fmt.Fprintf(os.Stderr, "charly: --host %q: bootstrap Charly endpoint: %v\n", cli.Host, err)
		return 1
	}
	if remoteBin != "charly" {
		// The venue's PATH charly was absent or older, so the remote command
		// runs a replica of THIS local binary — a version skew worth one line.
		fmt.Fprintf(os.Stderr, "charly: --host %q: venue charly absent/older; running replicated controller binary at %s\n", cli.Host, remoteBin)
	}
	sshArgs, err := sshCmdArgsWithEndpoint(target, remoteBin, cli.HostIdentityFile, cli.HostOption, remoteArgv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "charly: --host %q: %v\n", cli.Host, err)
		return 2
	}
	cmd := exec.Command("ssh", sshArgs...)
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
	cfg, err := kit.LoadRuntimeConfig()
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
		if a == "--host" || a == "--host-identity-file" || a == "--host-option" || a == "--dir" || a == "-C" || a == "--repo" {
			skipNext = true
			continue
		}
		if strings.HasPrefix(a, "--host=") ||
			strings.HasPrefix(a, "--host-identity-file=") ||
			strings.HasPrefix(a, "--host-option=") ||
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
func sshCmdArgs(target string, remoteArgv []string) ([]string, error) {
	return sshCmdArgsWithEndpoint(target, "charly", "", nil, remoteArgv)
}

func sshCmdArgsWithEndpoint(target, remoteBinary, identityFile string, options, remoteArgv []string) ([]string, error) {
	destination, port, err := splitSSHTarget(target)
	if err != nil {
		return nil, err
	}
	args := make([]string, 0, 8+len(options)*2)
	if term.IsTerminal(int(os.Stdin.Fd())) {
		args = append(args, "-tt")
	}
	if port != "" {
		args = append(args, "-p", port)
	}
	if identityFile != "" {
		args = append(args, "-i", identityFile)
	}
	for _, option := range options {
		args = append(args, "-o", option)
	}
	remote := kit.ShellQuote(remoteBinary)
	for _, arg := range remoteArgv {
		remote += " " + kit.ShellQuote(arg)
	}
	args = append(args, destination, remote)
	return args, nil
}

// splitSSHTarget converts the documented user@host[:port] form into OpenSSH's
// argv representation. Ports are data, never embedded in the destination
// token; IPv6 literals use the standard bracketed host:port form.
func splitSSHTarget(target string) (destination, port string, err error) {
	if target == "" {
		return "", "", errors.New("empty SSH target")
	}
	user := ""
	hostPort := target
	if at := strings.LastIndex(target, "@"); at >= 0 {
		if at == 0 || at == len(target)-1 {
			return "", "", fmt.Errorf("invalid SSH target %q", target)
		}
		user, hostPort = target[:at], target[at+1:]
	}
	host := hostPort
	switch {
	case strings.HasPrefix(hostPort, "["):
		switch {
		case strings.Contains(hostPort, "]:"):
			host, port, err = net.SplitHostPort(hostPort)
			if err != nil {
				return "", "", fmt.Errorf("invalid SSH target %q: %w", target, err)
			}
		case strings.HasSuffix(hostPort, "]"):
			host = strings.TrimSuffix(strings.TrimPrefix(hostPort, "["), "]")
		default:
			return "", "", fmt.Errorf("invalid SSH target %q", target)
		}
	case strings.Count(hostPort, ":") == 1:
		host, port, err = net.SplitHostPort(hostPort)
		if err != nil {
			return "", "", fmt.Errorf("invalid SSH target %q: %w", target, err)
		}
	}
	if host == "" {
		return "", "", fmt.Errorf("invalid SSH target %q: empty host", target)
	}
	if port != "" {
		value, parseErr := strconv.ParseUint(port, 10, 16)
		if parseErr != nil || value == 0 {
			return "", "", fmt.Errorf("invalid SSH target %q: port must be between 1 and 65535", target)
		}
	}
	destination = host
	if strings.Contains(host, ":") {
		destination = "[" + host + "]"
	}
	if user != "" {
		destination = user + "@" + destination
	}
	return destination, port, nil
}
