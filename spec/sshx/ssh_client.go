package sshx

// Package sshx (github.com/opencharly/spec/sshx, #55 step1) is the in-process
// SSH client + tunnel — the SOLE golang.org/x/crypto/ssh boundary in the whole
// tree. It is a fabric slice of the spec contract module, imported ONLY by the
// SSH-forwarding call sites (charly's tunnels, plugin-vm), never by sdk/kit or
// sdk/vmshared, so the executors + every deploy/check plugin that import kit
// stay x/crypto-free (Rule 2: heavy deps live only in the slice that needs them).
//
// The SSH target vocabulary (spec.SSHTarget/ParseSSHTarget) is pure and lives in
// github.com/opencharly/spec/spec (ssh_target.go); this file reuses it via
// spec.SSHTarget.
//
// The executor used by `charly fleet add vm:<name>` (deploy_executor_ssh.go)
// keeps shelling out to the system `ssh` binary — that path wants the user's
// ~/.ssh/config, ControlMaster, agent forwarding, etc. This is a parallel,
// narrower client just for in-process port forwarding.

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/opencharly/spec/spec"
)

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
func SSHClientConfig(t spec.SSHTarget) (*ssh.ClientConfig, error) {
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
func DialSSH(t spec.SSHTarget) (*ssh.Client, error) {
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
