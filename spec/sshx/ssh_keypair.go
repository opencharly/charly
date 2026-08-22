package sshx

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
)

// ssh_keypair.go — the shared SSH-keypair-generation utility (R3 hoist, #118 F6 vm-lifecycle move,
// coneB-vmlifecycle): was byte-identically duplicated in charly core (vm_backend_lifecycle.go, for
// the pod-config SSH-key seam) AND candy/plugin-vm (vm.go, for VM cloud-init SSH keys) — a
// coincidentally-identical need, now single-sourced here. Lives in sshx (NOT sdk/kit): kit is
// imported near-universally, and golang.org/x/crypto/ssh is NOT already a kit dependency — adding
// it there broke every plugin whose go.sum lacks the transitive package (RCA'd live, this cutover:
// ~6 example/test plugin builds failed on "missing go.sum entry for ... golang.org/x/crypto/ssh").
// sshx already carries this exact dependency (ssh_tunnel.go/ssh_client.go) and is consumed only by
// the modules that already need SSH (charly, candy/plugin-vm, candy/plugin-ssh) — the SAME
// dependency-containment reasoning charly/check_graphics_endpoint.go's floor clause documents for
// its own x/crypto/ssh tunnel usage.

// ResolveSSHPubKey resolves the --ssh-key flag to a public key string.
// Values: "auto" (default ~/.ssh key), "none", "generate", or a file path.
// generateDir is the directory where generated keypairs are stored (only used for "generate").
func ResolveSSHPubKey(flag, generateDir string) (string, error) {
	switch flag {
	case "none":
		return "", nil
	case "auto":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cannot determine home directory: %w", err)
		}
		for _, name := range []string{"id_ed25519.pub", "id_rsa.pub", "id_ecdsa.pub"} {
			path := filepath.Join(home, ".ssh", name)
			if data, err := os.ReadFile(path); err == nil {
				pubkey := strings.TrimSpace(string(data))
				fmt.Fprintf(os.Stderr, "Using SSH key from %s\n", path)
				return pubkey, nil
			}
		}
		return "", fmt.Errorf("no SSH public key found in ~/.ssh/ — use --ssh-key <path> or --ssh-key generate")
	case "generate":
		if err := os.MkdirAll(generateDir, 0755); err != nil {
			return "", err
		}
		pubkey, err := GenerateSSHKeypair(generateDir)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(os.Stderr, "Generated SSH keypair in %s\n", generateDir)
		return pubkey, nil
	default:
		// Treat as file path
		data, err := os.ReadFile(flag)
		if err != nil {
			return "", fmt.Errorf("reading SSH public key %s: %w", flag, err)
		}
		return strings.TrimSpace(string(data)), nil
	}
}

// GenerateSSHKeypair creates an ed25519 keypair in the given directory.
// Returns the public key in authorized_keys format. Idempotent: when
// the .pub file already exists in dir, the existing public key is
// read and returned without generating a new pair (so multiple VM
// lifecycle calls — build, create, start — use the same identity).
func GenerateSSHKeypair(dir string) (string, error) {
	pubPath := filepath.Join(dir, "id_ed25519.pub")
	if existing, err := os.ReadFile(pubPath); err == nil {
		return strings.TrimSpace(string(existing)), nil
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", fmt.Errorf("generating ed25519 key: %w", err)
	}

	privKey, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		return "", fmt.Errorf("marshaling private key: %w", err)
	}
	privPEM := pem.EncodeToMemory(privKey)
	if err := os.WriteFile(filepath.Join(dir, "id_ed25519"), privPEM, 0600); err != nil {
		return "", err
	}

	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return "", fmt.Errorf("creating SSH public key: %w", err)
	}
	authorizedKey := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub)))
	if err := os.WriteFile(filepath.Join(dir, "id_ed25519.pub"), []byte(authorizedKey+"\n"), 0644); err != nil {
		return "", err
	}

	return authorizedKey, nil
}

// ContainerSSHKeyDir returns the directory for storing a container's SSH keypair.
func ContainerSSHKeyDir(name string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "charly", "ssh", name), nil
}
