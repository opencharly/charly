package vmshared

// Shared SSH client plumbing for programmatic TCP/UNIX forwarding
// (charly/ssh_tunnel.go) and for SPICE/VNC auto-tunneling inside the
// `charly check` commands. Built on `golang.org/x/crypto/ssh`, which is
// already a transitive dependency.
//
// The executor used by `charly bundle add vm:<name>` (in
// deploy_executor_ssh.go) keeps shelling out to the system `ssh`
// binary — that path wants to inherit the user's ~/.ssh/config,
// ControlMaster, agent forwarding, and everything else OpenSSH
// knows how to do. This file is a parallel, narrower client just
// for in-process port forwarding.

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
)

// SSHTarget is the parsed form of an ssh-style target string.
// Examples:
//   - "host"                     → {User: $USER, Host: "host", Port: 22}
//   - "user@host"                → {User: "user", Host: "host", Port: 22}
//   - "user@host:2222"           → {User: "user", Host: "host", Port: 2222}
//   - "qemu+ssh://user@host/…"   → parsed via ParseLibvirtSSHURI (not here)
type SSHTarget struct {
	User string
	Host string
	Port int
}

// ParseSSHTarget accepts "[user@]host[:port]" and fills defaults:
//   - User: $USER
//   - Port: 22
func ParseSSHTarget(s string) (SSHTarget, error) {
	if s == "" {
		return SSHTarget{}, fmt.Errorf("empty ssh target")
	}
	t := SSHTarget{Port: 22}
	rest := s
	if user, after, ok := strings.Cut(rest, "@"); ok {
		t.User = user
		rest = after
	}
	if i := strings.LastIndex(rest, ":"); i >= 0 {
		t.Host = rest[:i]
		p, err := strconv.Atoi(rest[i+1:])
		if err != nil {
			return SSHTarget{}, fmt.Errorf("invalid port in %q: %w", s, err)
		}
		t.Port = p
	} else {
		t.Host = rest
	}
	if t.Host == "" {
		return SSHTarget{}, fmt.Errorf("missing host in ssh target %q", s)
	}
	if t.User == "" {
		t.User = currentUsername()
	}
	return t, nil
}

// String renders the canonical "user@host:port" form.
func (t SSHTarget) String() string {
	if t.Port == 22 {
		return fmt.Sprintf("%s@%s", t.User, t.Host)
	}
	return fmt.Sprintf("%s@%s:%d", t.User, t.Host, t.Port)
}

// SSHClientConfig builds an *ssh.ClientConfig for the given target
// using (in order):
//   - SSH_AUTH_SOCK — ssh-agent keys, if available.
//   - ~/.ssh/id_ed25519, ~/.ssh/id_rsa, ~/.ssh/id_ecdsa — any
//     readable key file (unencrypted; we don't prompt for passphrases
//     here — the user's normal workflow should keep keys in the agent).
//
// Host-key checking honors ~/.ssh/known_hosts when present; otherwise
// it falls back to InsecureIgnoreHostKey. Callers that need strict
// verification should ensure the agent path is available.
func SSHClientConfig(t SSHTarget) (*ssh.ClientConfig, error) {
	auths, err := SSHAuthMethods()
	if err != nil {
		return nil, err
	}
	if len(auths) == 0 {
		return nil, fmt.Errorf("no SSH auth methods available (no ssh-agent and no readable keys in ~/.ssh/)")
	}
	cfg := &ssh.ClientConfig{
		User:            t.User,
		Auth:            auths,
		HostKeyCallback: sshHostKeyCallback(),
		Timeout:         15 * time.Second,
	}
	return cfg, nil
}

// SSHAuthMethods probes the user's environment for usable SSH auth.
// Agent first, then the three common Ed25519/RSA/ECDSA private keys.
func SSHAuthMethods() ([]ssh.AuthMethod, error) { //nolint:unparam // error return kept for interface/API stability
	var methods []ssh.AuthMethod
	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		if conn, err := net.Dial("unix", sock); err == nil {
			methods = append(methods, ssh.PublicKeysCallback(agent.NewClient(conn).Signers))
		}
	}
	home, err := os.UserHomeDir()
	if err == nil {
		for _, name := range []string{"id_ed25519", "id_rsa", "id_ecdsa"} {
			path := filepath.Join(home, ".ssh", name)
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			signer, err := ssh.ParsePrivateKey(data)
			if err != nil {
				// Encrypted or unsupported — skip silently; the
				// agent path should cover this case.
				continue
			}
			methods = append(methods, ssh.PublicKeys(signer))
		}
	}
	return methods, nil
}

// sshHostKeyCallback returns a callback that checks against
// ~/.ssh/known_hosts when the file is readable. If unreadable (no
// such file yet, first connection), falls back to
// InsecureIgnoreHostKey — matching OpenSSH's default behavior with
// StrictHostKeyChecking=accept-new on modern distros.
func sshHostKeyCallback() ssh.HostKeyCallback {
	home, err := os.UserHomeDir()
	if err != nil {
		return ssh.InsecureIgnoreHostKey()
	}
	kh := filepath.Join(home, ".ssh", "known_hosts")
	cb, err := knownhosts.New(kh)
	if err != nil {
		return ssh.InsecureIgnoreHostKey()
	}
	return cb
}

// DialSSH opens an authenticated SSH client connection to the
// target. Caller must Close.
func DialSSH(t SSHTarget) (*ssh.Client, error) {
	cfg, err := SSHClientConfig(t)
	if err != nil {
		return nil, err
	}
	addr := fmt.Sprintf("%s:%d", t.Host, t.Port)
	client, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		return nil, fmt.Errorf("ssh dial %s: %w", addr, err)
	}
	return client, nil
}

// currentUsername returns $USER or falls back to "charly" if neither
// $USER nor $LOGNAME is set.
func currentUsername() string {
	for _, env := range []string{"USER", "LOGNAME"} {
		if v := os.Getenv(env); v != "" {
			return v
		}
	}
	return "charly"
}
