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
	"strconv"
	"strings"
	"syscall"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
	"github.com/opencharly/sdk/vmshared"
	"golang.org/x/crypto/ssh"
)

// vm_backend_lifecycle.go — the core-side VM BACKEND + LIFECYCLE machinery that
// survives the P10 VM-CLI move to candy/plugin-vm. The CLI command handlers
// (VmCmd + the Create/Start/Stop/Destroy/List/Console/Ssh tree) moved into the
// plugin; these shared helpers stay because non-CLI core consumers still call
// them: the config-resolve seam (host_build_config_resolve.go), the resource
// arbiter (preempt.go — startVM/stopVM/vmDir), the sibling-member + bed runners
// (bundle_members.go / check_bed_run.go — startLibvirtUserSession), and the
// container SSH-key helpers (config_image.go / vm_cloud_image.go). The K5 vm
// status collector (candy/plugin-substrate/status_vm.go) does NOT call
// resolveVmBackend — it reaches candy/plugin-vm's verb:libvirt directly over
// InvokeProvider and gates on vmshared.LibvirtSessionSocket() instead.

// vmName returns the VM name for an image and optional instance.
func vmName(box, instance string) string {
	name := "charly-" + box
	if instance != "" {
		name += "-" + instance
	}
	return name
}

// vmDir returns the directory for storing VM state (QEMU backend). Routes through
// vmshared.VmStateRoot (bed-robustness batch item 6 — the CHARLY_VM_STATE_DIR worktree-scoping
// override) rather than a hardcoded literal, so every VM state path in this process honors the
// same override every other VM code path does.
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

// startVM starts a previously-created VM by image+instance, dispatching by
// backend (libvirt domain start / re-exec the stored qemu command). Shared
// by the plugin's `charly vm start` handler and the resource arbiter
// (charly/preempt.go) so the holder-restart path runs the exact same lifecycle
// code as `charly vm start`.
func startVM(box, instance string) error {
	rt, err := kit.ResolveRuntime()
	if err != nil {
		return err
	}

	backend, err := resolveVmBackend(vmConfiguredBackend(box, rt.VmBackend))
	if err != nil {
		return err
	}

	name := vmName(box, instance)

	switch backend {
	case "libvirt":
		raw, ok := invokeVmPlugin("start", name, "")
		if !ok {
			return fmt.Errorf("VM %s: vm plugin unavailable (go-libvirt is out-of-process)", name)
		}
		if e := vmPluginOpError(raw); e != "" {
			return fmt.Errorf("starting VM %s: %s", name, e)
		}
		if vmPluginOpFlag(raw, "already_running") {
			fmt.Fprintf(os.Stderr, "VM %s is already running\n", name)
		} else {
			fmt.Fprintf(os.Stderr, "Started VM %s\n", name)
		}
	case "qemu":
		dir, err := vmDir()
		if err != nil {
			return err
		}
		stateDir := filepath.Join(dir, name)
		cmdFile := filepath.Join(stateDir, "command")
		data, err := os.ReadFile(cmdFile)
		if err != nil {
			return fmt.Errorf("VM %s not found — run 'charly vm create %s' first", name, box)
		}
		parts := strings.Fields(string(data))
		if len(parts) < 2 {
			return fmt.Errorf("invalid stored command for VM %s", name)
		}
		cmd := exec.Command(parts[0], parts[1:]...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("qemu start failed: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Started VM %s\n", name)
	}
	return nil
}

// stopVM stops a running VM by image+instance. force=false performs a
// graceful ACPI shutdown (disk + definition preserved — the "stopped, but
// not depleted" semantic the resource arbiter relies on); force=true
// destroys/kills it. Shared by the plugin's `charly vm stop` handler and the
// resource arbiter (charly/preempt.go), which always calls it with force=false
// so a preempted holder is gracefully shut down and remains restartable.
func stopVM(box, instance string, force bool) error {
	rt, err := kit.ResolveRuntime()
	if err != nil {
		return err
	}

	backend, err := resolveVmBackend(vmConfiguredBackend(box, rt.VmBackend))
	if err != nil {
		return err
	}

	name := vmName(box, instance)

	switch backend {
	case "libvirt":
		raw, ok := invokeVmPluginEnv(spec.VmPluginEnv{VmOp: "stop", VmName: name, Force: force})
		if !ok {
			return fmt.Errorf("VM %s: vm plugin unavailable (go-libvirt is out-of-process)", name)
		}
		if e := vmPluginOpError(raw); e != "" {
			return fmt.Errorf("stopping VM %s: %s", name, e)
		}
		fmt.Fprintf(os.Stderr, "Stopped VM %s\n", name)
	case "qemu":
		dir, err := vmDir()
		if err != nil {
			return err
		}
		stateDir := filepath.Join(dir, name)
		if force {
			// Try QMP quit first, fall back to process kill
			if err := qemuForceShutdown(stateDir); err != nil {
				// Fallback: kill via PID
				killQemuByPID(stateDir)
			}
		} else {
			// Graceful ACPI shutdown via QMP
			if err := qemuGracefulShutdown(stateDir); err != nil {
				// Fallback: SIGTERM via PID
				pidFile := filepath.Join(stateDir, "qemu.pid")
				if data, readErr := os.ReadFile(pidFile); readErr == nil {
					if pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data))); parseErr == nil {
						if proc, findErr := os.FindProcess(pid); findErr == nil {
							_ = proc.Signal(syscall.SIGTERM)
						}
					}
				}
			}
		}
		fmt.Fprintf(os.Stderr, "Stopped VM %s\n", name)
	}
	return nil
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
