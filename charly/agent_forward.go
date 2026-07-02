package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// AgentForwardMounts holds the resolved bind mounts and env vars needed to
// forward SSH and GPG agent sockets from the host into a container.
type AgentForwardMounts struct {
	Volumes []string // host:container[:options] — only for container CREATION
	Env     []string // KEY=VALUE — for both creation and exec
}

// ResolveAgentForwarding detects available agent sockets on the host and
// returns the bind mounts and environment variables needed to forward them
// into a container. deploy may be nil (no per-image overrides).
// containerHome is the home directory inside the container (e.g., "/root" or
// "/home/user") — determines where GPG expects its agent socket.
//
// Graceful degradation: logs warnings to stderr for missing sockets but
// never returns errors — missing agents are silently skipped.
func ResolveAgentForwarding(rt *ResolvedRuntime, deploy *BundleNode, containerHome string) AgentForwardMounts {
	var result AgentForwardMounts

	// SSH agent forwarding
	forwardSSH := rt.ForwardSshAgent
	if deploy != nil && deploy.ForwardSshAgent != nil {
		forwardSSH = *deploy.ForwardSshAgent
	}
	if forwardSSH {
		if vol, env, ok := resolveSSHAgentForward(); ok {
			result.Volumes = append(result.Volumes, vol)
			result.Env = append(result.Env, env)
		}
	}

	// GPG agent forwarding
	forwardGPG := rt.ForwardGpgAgent
	if deploy != nil && deploy.ForwardGpgAgent != nil {
		forwardGPG = *deploy.ForwardGpgAgent
	}
	if forwardGPG {
		if vol, ok := resolveGPGAgentForward(containerHome); ok {
			result.Volumes = append(result.Volumes, vol)
		}
	}

	return result
}

// resolveSSHAgentForward checks for SSH_AUTH_SOCK and returns the mount + env var.
func resolveSSHAgentForward() (volume, envVar string, ok bool) {
	sock := os.Getenv("SSH_AUTH_SOCK")
	if sock == "" {
		return "", "", false
	}

	if _, err := os.Stat(sock); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: SSH_AUTH_SOCK=%s does not exist, skipping SSH agent forwarding\n", sock)
		return "", "", false
	}

	const containerPath = "/run/host-ssh-auth.sock"
	return sock + ":" + containerPath, "SSH_AUTH_SOCK=" + containerPath, true
}

// resolveGPGAgentForward forwards the host's GPG agent socket into the
// container at the standard agent socket path for the container's home dir.
//
// This mirrors SSH's RemoteForward pattern:
//
//	Host S.gpg-agent → Container $HOME/.gnupg/S.gpg-agent
//
// Uses the primary agent socket (not the extra/restricted one) because
// containers are local and trusted — they need full agent capabilities
// including decryption (required for .secrets workflow).
// The container has its own keyring — no host keyring is mounted.
// No GPG agent or keyboxd runs inside the container.
func resolveGPGAgentForward(containerHome string) (volume string, ok bool) {
	agentSocket := gpgAgentSocket()
	if agentSocket == "" {
		return "", false
	}

	if _, err := os.Stat(agentSocket); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: GPG agent socket %s does not exist, skipping GPG agent forwarding\n", agentSocket)
		return "", false
	}

	// Container target: $HOME/.gnupg/S.gpg-agent inside the container.
	// Containers typically don't have XDG_RUNTIME_DIR (no systemd user session),
	// so GPG falls back to using GNUPGHOME or $HOME/.gnupg for socket placement.
	containerSocket := filepath.Join(containerHome, ".gnupg", "S.gpg-agent")
	return agentSocket + ":" + containerSocket, true
}

// gpgAgentSocket returns the path to the host's GPG agent primary socket.
// Uses gpgconf if available, falls back to the standard systemd path.
func gpgAgentSocket() string {
	if out, err := exec.Command("gpgconf", "--list-dirs", "agent-socket").Output(); err == nil {
		if path := strings.TrimSpace(string(out)); path != "" {
			return path
		}
	}

	// Fallback: standard systemd path
	xdgRuntime := os.Getenv("XDG_RUNTIME_DIR")
	if xdgRuntime == "" {
		xdgRuntime = fmt.Sprintf("/run/user/%d", os.Getuid())
	}
	return filepath.Join(xdgRuntime, "gnupg", "S.gpg-agent")
}
