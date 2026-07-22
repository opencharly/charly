package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/opencharly/sdk/spec"
	"github.com/opencharly/sdk/vmshared"
	"golang.org/x/crypto/ssh"
)

// vm_backend_lifecycle.go — the core-side VM BACKEND + LIFECYCLE machinery that
// survives the P10 VM-CLI move to candy/plugin-vm. The CLI command handlers
// (VmCmd + the Create/Start/Stop/Destroy/List/Console/Ssh tree) moved into the
// plugin; these shared helpers stay because non-CLI core consumers still call
// them: the config-resolve seam (host_build_config_resolve.go — vmConfiguredBackend),
// the sibling-member + bed runners (bundle_members.go / check_bed_run.go —
// startLibvirtUserSession), the container SSH-key helpers (config_image.go /
// vm_cloud_image.go), and the qcow2-VM host-build seam (host_build_vm_build.go —
// vmDir, for the per-entity state dir it creates before staging the disk/seed
// ISO). The FORMER resource-arbiter consumer (preempt.go's startVM/stopVM/vmName
// holder start/stop) moved into candy/plugin-preempt (FLOOR-SLIM-proper Unit-8)
// with its own vmName/start/stop implementation dispatching verb:libvirt
// directly via InvokeProvider — so those symbols, and their qemu-backend-only
// support cluster (vm_qemu_client.go, vm_plugin_client.go's op-reply decoders,
// vmshared_aliases.go's killQemuByPID), are DELETED here (R5): the CLI's own
// startVM/stopVM equivalent lives entirely in candy/plugin-vm and never called
// back into this file's copy. `vmDir` itself STAYS — host_build_vm_build.go is a
// genuinely separate, still-live consumer (confirmed via git grep across the
// FULL merged tree, including the just-landed bed-robustness batch) — and now
// routes through vmshared.VmStateRoot() (the CHARLY_VM_STATE_DIR worktree-scoping
// override bed-robustness's batch, charly#176, unified every other VM-state path
// onto), so candy/plugin-preempt's own vmDirPlugin (holder_dispatch.go) matches
// it symmetrically rather than duplicating the un-scoped literal. The K5 vm
// status collector (candy/plugin-substrate/status_vm.go) does NOT call
// resolveVmBackend — it reaches candy/plugin-vm's verb:libvirt directly over
// InvokeProvider and gates on vmshared.LibvirtSessionSocket() instead.

// vmDir returns the root directory for storing VM state (QEMU backend), honoring the
// CHARLY_VM_STATE_DIR worktree-scoping override (bed-robustness batch item 6) — the SAME
// override every other VM-state path in this process now goes through.
func vmDir() (string, error) {
	return vmshared.VmStateRoot()
}

// resolveVmBackend detects the available VM backend.
// Priority: libvirt → qemu
func resolveVmBackend(configured string) (string, error) {
	if configured == "libvirt" || configured == "auto" {
		// Spawn the libvirt session daemon BEFORE probing for its socket.
		// On hosts that ship no persistent virtqemud.socket (Arch/CachyOS),
		// the socket exists only AFTER a client triggers libvirt's on-demand
		// autospawn — so a COLD os.Stat() false-negatives a fully working
		// libvirt and silently falls back to qemu (or errors on
		// `backend: libvirt`). resolveVmBackend is called from many verbs
		// (create/build/start/stop/destroy/console) and only some had a
		// preceding spawn, so the detection was nondeterministic per-verb;
		// spawning HERE makes every caller detect uniformly. The call is
		// idempotent + best-effort (a no-op when libvirt is absent — virsh
		// LookPath simply fails). NOTE: a `systemctl is-active <service>`
		// check is the WRONG probe — socket-activated / autospawn daemons
		// report the SERVICE inactive while libvirt is fully usable; the
		// socket (or a real connection) is the only valid signal.
		startLibvirtUserSession()
		picked, probed := libvirtSessionSocketWithProbes()
		// `picked` is the last-resort dial target; we still need to
		// confirm it exists. The earlier probes (in `probed`) ARE
		// already stat'd inside libvirtSessionSocketWithProbes, but
		// that function returns the legacy path when neither exists,
		// so we re-stat here to be sure.
		if _, err := os.Stat(picked); err == nil {
			return "libvirt", nil
		}
		if configured == "libvirt" {
			var trail strings.Builder
			for _, p := range probed {
				_, err := os.Stat(p)
				if err == nil {
					fmt.Fprintf(&trail, "\n  %s — found", p)
				} else {
					fmt.Fprintf(&trail, "\n  %s — not found", p)
				}
			}
			return "", fmt.Errorf(
				"libvirt backend requires libvirt session daemon (probed:%s\n"+
					"configure libvirt session daemon or run: charly settings set vm.backend qemu)",
				trail.String(),
			)
		}
	}
	if configured == "qemu" || configured == "auto" {
		qemuBin := qemuSystemBinary()
		if _, err := exec.LookPath(qemuBin); err == nil {
			return "qemu", nil
		}
		if configured == "qemu" {
			return "", fmt.Errorf("qemu backend requires %s", qemuBin)
		}
	}
	return "", fmt.Errorf("no VM backend available (install libvirt or qemu-system)")
}

// vmConfiguredBackend returns the backend string to feed resolveVmBackend for
// a vm entity: the entity's `backend:` pin (VmSpec.Backend) when set, else the
// global vm.backend setting. THE single source so EVERY vm verb (create /
// destroy / start / stop / console) resolves the SAME backend for a given
// entity. Without it, `charly vm create` (honoring the pin) and `charly vm destroy`
// (using the global setting) can pick DIFFERENT backends — the destroy then
// silently operates on the wrong backend's (non-existent) domain and leaves
// the created libvirt domain running, surfacing as "domain already exists" on
// the next create (the check-k3s-vm `charly update` failure when vm.backend=qemu
// but the bed pins backend: libvirt).
func vmConfiguredBackend(vmName, rtBackend string) string {
	if vmName == "" {
		return rtBackend
	}
	// Resolved through the generic "deploy-entity-resolve" host-builder (FINAL/K5 unit 6a)
	// instead of LoadUnified directly — this file is core-only, so it calls the
	// host-builder function in-process (no HostBuild/Executor round trip needed, unlike a
	// plugin caller).
	reply, err := hostBuildDeployEntityResolve(context.Background(), spec.DeployEntityResolveRequest{Kind: "vm", Name: vmName}, buildEngineContext{})
	if err != nil || len(reply.EntityJSON) == 0 {
		return rtBackend
	}
	var vm spec.ResolvedVm
	if err := json.Unmarshal(reply.EntityJSON, &vm); err != nil || vm.Backend == "" {
		return rtBackend
	}
	return vm.Backend
}

// startLibvirtUserSession ensures the libvirt user-session daemon is
// running. Modular libvirt's `virtqemud --timeout=120` auto-exits
// after 120 s of idle, so consecutive `charly check libvirt …` calls
// spaced wider than that find the socket gone.
//
// Three start mechanisms tried in order, all best-effort:
//
//  1. `systemctl --user start virtqemud.service` — preferred when the
//     unit is installed (Debian/Ubuntu mostly).
//  2. `systemctl --user start libvirtd.service` — legacy monolithic
//     libvirt.
//  3. `virsh -c qemu:///session list` — works on Arch and any host
//     where libvirt installs WITHOUT systemd user units. virsh
//     dispatches to `virt-ssh-helper` / `virtqemud` directly, which
//     spawns the daemon and creates `/run/user/$UID/libvirt/
//     virtqemud-sock` on first connect.
//
// The function silently ignores all failures. Two outcomes:
//   - Daemon now running → caller's subsequent socket dial succeeds.
//   - Daemon not installable (no libvirt on this host) → caller's
//     downstream socket dial returns "no such file or directory",
//     which surfaces the real error.
//
// Reason for best-effort: don't block legitimate non-libvirt users.
//
// Package-level var (not a plain func) so hermetic tests can stub it to a
// no-op — resolveVmBackend now calls it before probing the socket, and an
// un-stubbed real spawn would create a socket inside a test's temp
// XDG_RUNTIME_DIR and defeat "no socket" fixtures (see stubNoLibvirtSpawn).
var startLibvirtUserSession = func() {
	// Try systemd user-units first.
	for _, unit := range []string{"virtqemud.service", "libvirtd.service"} {
		// Idempotent: systemctl start on an already-active unit is a no-op.
		_ = exec.Command("systemctl", "--user", "start", unit).Run()
	}
	// Fall back to virsh-driven spawn for Arch-class hosts that ship
	// libvirt WITHOUT systemd user units (the binary is launched on-
	// demand via D-Bus or virt-ssh-helper). `list` is read-only and
	// returns 0 even with no domains.
	if _, err := exec.LookPath("virsh"); err == nil {
		_ = exec.Command("virsh", "-c", "qemu:///session", "list").Run()
	}
}

// resolveSSHPubKey resolves the --ssh-key flag to a public key string.
// Values: "auto" (default ~/.ssh key), "none", "generate", or a file path.
// generateDir is the directory where generated keypairs are stored (only used for "generate").
func resolveSSHPubKey(flag, generateDir string) (string, error) {
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
		pubkey, err := generateSSHKeypair(generateDir)
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

// containerSSHKeyDir returns the directory for storing container SSH keypairs.
func containerSSHKeyDir(name string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "charly", "ssh", name), nil
}

// generateSSHKeypair creates an ed25519 keypair in the given directory.
// Returns the public key in authorized_keys format. Idempotent: when
// the .pub file already exists in dir, the existing public key is
// read and returned without generating a new pair (so multiple VM
// lifecycle calls — build, create, start — use the same identity).
func generateSSHKeypair(dir string) (string, error) {
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
