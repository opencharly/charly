package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"

	"golang.org/x/crypto/ssh"
	"golang.org/x/term"
)

const libvirtSessionURI = "qemu:///session"

// VmCmd groups VM management subcommands.
type VmCmd struct {
	Build    VmBuildCmd    `cmd:"" help:"Build QCOW2/RAW disk image from bootc container"`
	Clone    VmCloneCmd    `cmd:"" help:"Clone a new VM from another VM's snapshot (writes a kind:vm declaration)"`
	Console  VmConsoleCmd  `cmd:"" help:"Attach to VM serial console"`
	CpImage  VmCpBoxCmd    `cmd:"" name:"cp-box" help:"Load a host image into a running VM guest's podman storage"`
	Create   VmCreateCmd   `cmd:"" help:"Create a VM from a disk image"`
	Destroy  VmDestroyCmd  `cmd:"" help:"Remove VM definition and optionally delete disk"`
	Gpu      VmGpuCmd      `cmd:"" help:"Inspect host VFIO/GPU-passthrough readiness (status, list)"`
	Import   VmImportCmd   `cmd:"" help:"Adopt an existing libvirt-managed VM into charly configuration"`
	List     VmListCmd     `cmd:"" help:"List VMs and their status"`
	Scp      VmScpCmd      `cmd:"" help:"Copy a local file into a running VM guest over SSH"`
	Snapshot VmSnapshotCmd `cmd:"" help:"Manage VM snapshots (create, list, delete, revert, promote)"`
	Ssh      VmSshCmd      `cmd:"" help:"SSH into a VM"`
	Start    VmStartCmd    `cmd:"" help:"Start a VM"`
	Stop     VmStopCmd     `cmd:"" help:"Stop a VM (graceful shutdown)"`
}

// vmName returns the VM name for an image and optional instance.
func vmName(box, instance string) string {
	name := "charly-" + box
	if instance != "" {
		name += "-" + instance
	}
	return name
}

// vmDir returns the directory for storing VM state (QEMU backend).
func vmDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "charly", "vm"), nil
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
	if dir, err := os.Getwd(); err == nil {
		if uf, ok, _ := LoadUnified(dir); ok && uf != nil && uf.VM != nil {
			if body, hit := uf.VM[vmName]; hit {
				if spec, _ := resolveVmViaPlugin(body); spec != nil && spec.Backend != "" {
					return spec.Backend
				}
			}
		}
	}
	return rtBackend
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

// ensureBootAutostartPrereqs makes a qemu:///session domain actually start at
// host boot. Two pieces are required:
//
//  1. Lingering — so the invoking user's systemd instance starts at boot
//     (without a login session). Idempotent.
//  2. A boot trigger that starts the domain. libvirt's own per-domain autostart
//     flag (set by the caller) only fires once the SESSION virtqemud is running,
//     and there is no portable user-level virtqemud.socket to socket-activate it
//     at boot — Arch/CachyOS ships none. So instead of relying on a shipped
//     socket unit, we generate a per-VM user systemd oneshot that runs
//     `virsh -c qemu:///session start <domain>` at boot; virsh spawns the
//     session daemon on demand and starts the (already-defined) domain. This is
//     deterministic and cross-distro.
//
// Best-effort with actionable warnings — the libvirt autostart flag is already
// set by the caller, so a failure here only loses the boot trigger.
func ensureBootAutostartPrereqs(domainName string) {
	username := currentUsername()
	if username != "" && !lingerEnabled(username) {
		if err := exec.Command("loginctl", "enable-linger", username).Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not enable systemd linger for %s (%v); the VM will not autostart at boot until you run: loginctl enable-linger %s\n", username, err, username)
		} else {
			fmt.Fprintf(os.Stderr, "Enabled systemd linger for %s (user session persists across logout so the VM autostarts at boot)\n", username)
		}
	}
	if err := writeAutostartUserUnit(domainName); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not install the boot-autostart user unit for %s (%v); the VM may not start at boot\n", domainName, err)
	}
}

// autostartUnitName is the per-domain user unit that starts a session VM at boot.
func autostartUnitName(domainName string) string {
	return "charly-autostart-" + domainName + ".service"
}

// writeAutostartUserUnit writes + enables the per-VM boot-autostart user unit.
func writeAutostartUserUnit(domainName string) error {
	cfgDir, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	unitDir := filepath.Join(cfgDir, "systemd", "user")
	if err := os.MkdirAll(unitDir, 0o755); err != nil {
		return err
	}
	virsh, err := exec.LookPath("virsh")
	if err != nil || virsh == "" {
		virsh = "virsh"
	}
	unit := fmt.Sprintf(`[Unit]
Description=OpenCharly autostart for libvirt session domain %[1]s
After=default.target

[Service]
Type=oneshot
ExecStart=/bin/sh -c 'exec %[2]s -c qemu:///session start %[1]s 2>/dev/null || true'
RemainAfterExit=yes

[Install]
WantedBy=default.target
`, domainName, virsh)
	unitName := autostartUnitName(domainName)
	if err := os.WriteFile(filepath.Join(unitDir, unitName), []byte(unit), 0o644); err != nil {
		return err
	}
	_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
	if err := exec.Command("systemctl", "--user", "enable", unitName).Run(); err != nil {
		return fmt.Errorf("systemctl --user enable %s: %w", unitName, err)
	}
	fmt.Fprintf(os.Stderr, "Installed boot-autostart user unit %s (starts %s at boot under the lingering session)\n", unitName, domainName)
	return nil
}

// removeAutostartUserUnit disables + deletes the per-domain boot-autostart user
// unit, if present. Idempotent — silent when there is nothing to remove.
func removeAutostartUserUnit(domainName string) {
	unitName := autostartUnitName(domainName)
	_ = exec.Command("systemctl", "--user", "disable", unitName).Run()
	cfgDir, err := os.UserConfigDir()
	if err != nil {
		return
	}
	unitPath := filepath.Join(cfgDir, "systemd", "user", unitName)
	if _, statErr := os.Stat(unitPath); statErr == nil {
		_ = os.Remove(unitPath)
		_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
		fmt.Fprintf(os.Stderr, "Removed boot-autostart user unit %s\n", unitName)
	}
}

// lingerEnabled reports whether systemd user lingering is already on for
// the given user, so we don't shell out to enable it redundantly.
func lingerEnabled(username string) bool {
	out, err := exec.Command("loginctl", "show-user", username, "--property=Linger").Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "Linger=yes"
}

// --- VmCreateCmd ---

// VmCreateCmd creates a VM from a QCOW2 disk image.
type VmCreateCmd struct {
	Box             string `arg:"" help:"Box name"`
	Ram             string `long:"ram" help:"Override RAM size (e.g. 4G, 8192M)"`
	Cpus            int    `long:"cpus" help:"Override CPU count"`
	Instance        string `short:"i" long:"instance" help:"Instance name"`
	SshKey          string `long:"ssh-key" default:"auto" help:"SSH public key: path to .pub file, 'auto' (default ~/.ssh key), 'generate', or 'none'"`
	AutoDetectFlags `embed:""`
}

func (c *VmCreateCmd) Run() error {
	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}

	// Best-effort: start the libvirt user-session daemon before backend
	// probe. Many fresh-user setups have virtqemud.service installed but
	// not started, which silently falls libvirt → qemu in resolveVmBackend
	// when backend is "auto", and produces a hard error when backend is
	// "libvirt". Auto-starting it gives a frictionless first-VM experience
	// without masking real problems: if the unit doesn't exist (libvirt
	// truly not installed), this is a no-op and the downstream gate
	// surfaces the actual issue.
	startLibvirtUserSession()

	// Resource arbitration: a standalone `charly vm create` of a VM that a
	// deploy/check node claims via requires_exclusive preempts the running
	// holders of that resource (persistent lease — released by `charly vm stop`/
	// `charly vm destroy`). No-op when an outer orchestrator already owns the lease
	// (CHARLY_PREEMPT_LEASE set, e.g. an check bed run) or when no claimant node
	// references this VM entity. See charly/preempt.go.
	claimant, claimantNode, hasClaimant := lookupVMClaimant(c.Box)
	if hasClaimant {
		if _, perr := acquireExclusiveForClaimant(claimant, claimantNode, false); perr != nil {
			return perr
		}
	}

	// --- New kind:vm entity path (D1, D4, D12) ---
	// Resolve the kind:vm entity FIRST so its `backend:` pin (when set)
	// overrides the global vm.backend setting BEFORE backend resolution —
	// the documented "pin backend: libvirt so the auto→qemu fallback can't
	// mask a missing daemon" behavior. (VmSpec.Backend was previously
	// absent, so the pin was silently dropped; now it is honored.)
	dir, _ := os.Getwd()
	var spec *VmSpec
	var resources map[string]*ResolvedResource
	if uf, ok, ufErr := LoadUnified(dir); ufErr == nil && ok {
		if uf.VM != nil {
			spec, _ = resolveVmViaPlugin(uf.VM[c.Box])
		}
		resources = uf.resolveResources()
	}
	backend, err := resolveVmBackend(vmConfiguredBackend(c.Box, rt.VmBackend))
	if err != nil {
		return err
	}
	if spec != nil {
		// VmSpec-driven create pipeline: RenderDomain for libvirt,
		// RenderQemuArgv for qemu. Uses output/qcow2/{disk,seed} produced
		// by `charly vm build` (the cloud_image branch of vm_build.go).
		// claimantNode + resources drive GPU auto-allocation (gpu_allocate.go).
		var claimantPtr *BundleNode
		if hasClaimant {
			claimantPtr = &claimantNode
		}
		return c.runVmSpecCreate(c.Box, spec, backend, claimantPtr, resources)
	}

	// Reached here = image is not a `kind: vm` entity, AND the legacy
	// BoxConfig.Vm / OCI LabelVm fallback was removed in the VM
	// hard-cutover. Tell the user what to do.
	_ = rt
	_ = backend
	return fmt.Errorf(
		"VM %q has no kind:vm entity in vm.yml.\n"+
			"  Declare one (optionally paired with a bootc image), e.g.:\n"+
			"      vm:\n"+
			"        %s-bootc:\n"+
			"          source: {kind: bootc, image: %s}",
		c.Box, c.Box, c.Box)
}

// parseRAMtoMB converts a RAM string like "4G" or "8192M" to megabytes.
func parseRAMtoMB(ram string) int {
	ram = strings.TrimSpace(ram)
	if strings.HasSuffix(ram, "G") || strings.HasSuffix(ram, "g") {
		val, err := strconv.Atoi(strings.TrimRight(ram, "Gg"))
		if err == nil {
			return val * 1024
		}
	}
	if strings.HasSuffix(ram, "M") || strings.HasSuffix(ram, "m") {
		val, err := strconv.Atoi(strings.TrimRight(ram, "Mm"))
		if err == nil {
			return val
		}
	}
	// Try plain number (assume MB)
	val, err := strconv.Atoi(ram)
	if err == nil {
		return val
	}
	return 4096 // fallback 4G
}

// --- VmStartCmd ---

type VmStartCmd struct {
	Box      string `arg:"" help:"Box name"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *VmStartCmd) Run() error {
	return startVM(c.Box, c.Instance)
}

// startVM starts a previously-created VM by image+instance, dispatching by
// backend (libvirt domain start / re-exec the stored qemu command). Shared
// by VmStartCmd.Run and the resource arbiter (charly/preempt.go) so the holder-
// restart path runs the exact same lifecycle code as `charly vm start`.
func startVM(box, instance string) error {
	rt, err := ResolveRuntime()
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

// --- VmStopCmd ---

type VmStopCmd struct {
	Box      string `arg:"" help:"Box name"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
	Force    bool   `long:"force" help:"Force stop (destroy) instead of graceful shutdown"`
}

func (c *VmStopCmd) Run() error {
	if err := stopVM(c.Box, c.Instance, c.Force); err != nil {
		return err
	}
	// Releasing a persistent exclusive claim on this VM restores any holder it
	// preempted (no-op if no lease / gated by an outer orchestrator).
	if claimant, _, ok := lookupVMClaimant(c.Box); ok {
		releaseResourceClaim(claimant)
	}
	return nil
}

// stopVM stops a running VM by image+instance. force=false performs a
// graceful ACPI shutdown (disk + definition preserved — the "stopped, but
// not depleted" semantic the resource arbiter relies on); force=true
// destroys/kills it. Shared by VmStopCmd.Run and the resource arbiter
// (charly/preempt.go), which always calls it with force=false so a preempted
// holder is gracefully shut down and remains restartable.
func stopVM(box, instance string, force bool) error {
	rt, err := ResolveRuntime()
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
		raw, ok := invokeVmPluginEnv(vmPluginEnv{VmOp: "stop", VmName: name, Force: force})
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

// --- VmDestroyCmd ---

type VmDestroyCmd struct {
	Box        string `arg:"" help:"Box name"`
	Instance   string `short:"i" long:"instance" help:"Instance name"`
	Disk       bool   `long:"disk" help:"Also delete the QCOW2 disk image"`
	KeepDeploy bool   `long:"keep-deploy" help:"Keep the charly.yml vm:<name> entry (default: remove it, like 'charly remove' for pods)"`
}

func (c *VmDestroyCmd) Run() error {
	// Releasing a persistent exclusive claim on this VM restores any preempted
	// holder once the claimant is gone (deferred so it runs on every exit;
	// no-op if no lease / gated by an outer orchestrator).
	if claimant, _, ok := lookupVMClaimant(c.Box); ok {
		defer releaseResourceClaim(claimant)
	}

	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}

	backend, err := resolveVmBackend(vmConfiguredBackend(c.Box, rt.VmBackend))
	if err != nil {
		return err
	}

	name := vmName(c.Box, c.Instance)

	switch backend {
	case "libvirt":
		// Destroy via the out-of-process vm plugin (the op graceful-stops + undefines, and is
		// idempotent on an already-gone domain); the charly.yml + ssh-config cleanup below still
		// runs so a lingering config whose domain is already destroyed is still removed.
		raw, ok := invokeVmPluginEnv(vmPluginEnv{VmOp: "destroy", VmName: name, DeleteDisk: c.Disk})
		if !ok {
			return fmt.Errorf("VM %s: vm plugin unavailable (go-libvirt is out-of-process)", name)
		}
		if e := vmPluginOpError(raw); e != "" {
			return fmt.Errorf("undefining VM %s: %s", name, e)
		}
		fmt.Fprintf(os.Stderr, "Destroyed VM %s\n", name)

	case "qemu":
		dir, err := vmDir()
		if err != nil {
			return err
		}
		stateDir := filepath.Join(dir, name)

		// Kill process — try QMP quit first, fall back to PID kill
		if err := qemuForceShutdown(stateDir); err != nil {
			killQemuByPID(stateDir)
		}

		// Remove state directory
		_ = os.RemoveAll(stateDir)
		fmt.Fprintf(os.Stderr, "Destroyed VM %s\n", name)
	}

	// Remove any boot-autostart user unit (the inverse of ensureBootAutostartPrereqs),
	// so a destroyed VM doesn't leave a unit that fails at boot. Idempotent.
	removeAutostartUserUnit(name)

	// Remove the managed ssh-config Host stanza (the inverse of what
	// `charly vm create` published). The libvirt/qemu domain `name` is
	// already the prefixed form ("charly-<image>" via vmName()), which IS
	// the alias — we use it directly without re-prefixing.
	if home, herr := os.UserHomeDir(); herr == nil {
		remaining, rerr := RemoveVmSshStanza(home, name)
		if rerr != nil {
			fmt.Fprintf(os.Stderr, "note: ssh-config stanza cleanup: %v\n", rerr)
		}
		if remaining == 0 {
			if rerr := RemoveSshConfigInclude(home); rerr != nil {
				fmt.Fprintf(os.Stderr, "note: ssh-config include cleanup: %v\n", rerr)
			}
		}
	}

	if c.Disk {
		// Remove only THIS VM's disk dir — never the shared parent (which
		// would delete every other VM's disk too).
		qcow2Dir := vmDiskDir(c.Box)
		_ = os.RemoveAll(qcow2Dir)
		fmt.Fprintf(os.Stderr, "Deleted disk images in %s\n", qcow2Dir)
	}

	// Remove the charly.yml vm:<name> entry — the inverse of the saveVmDeployState
	// that `charly bundle add vm:<name>` (and the ssh.port_auto vm-create persist)
	// wrote. Destroying the VM removes the deployment, so its config must not
	// linger; this is what made disposable check-bed VM entries accumulate (the
	// bed cleanup tears down via `charly vm destroy`). --keep-deploy preserves it for
	// a deliberate re-create, mirroring `charly remove --keep-deploy` for pods.
	if !c.KeepDeploy {
		deployName := "vm:" + deployKey(c.Box, c.Instance)
		if err := removeVmDeployEntry(deployName); err != nil {
			fmt.Fprintf(os.Stderr, "note: charly.yml entry cleanup (%s): %v\n", deployName, err)
		}
	}

	return nil
}

// --- VmListCmd ---

type VmListCmd struct {
	All          bool `short:"a" long:"all" help:"Show all VMs including stopped"`
	CleanOrphans bool `long:"clean-orphans" help:"Detect and undefine orphan libvirt domains (defined but no qcow2 backing or state dir)"`
}

func (c *VmListCmd) Run() error {
	if c.CleanOrphans {
		return c.runCleanOrphans()
	}

	// Backend-agnostic listing — probe BOTH libvirt and QEMU and merge.
	// Each probe is informational; a failure in one doesn't fail the
	// whole command. Pre-fix behavior was to bail when the configured
	// backend's probe failed, hiding running VMs in the OTHER backend.
	type vmRow struct {
		Name    string
		Backend string
		State   string
	}
	var rows []vmRow
	var probeNotes []string

	// libvirt probe via the out-of-process vm plugin (go-libvirt moved there).
	if raw, ok := invokeVmPlugin("list-domains", "", ""); ok {
		var domains []domainInfo
		if json.Unmarshal(raw, &domains) == nil {
			for _, d := range domains {
				rows = append(rows, vmRow{Name: d.Name, Backend: "libvirt", State: d.State})
			}
		} else {
			probeNotes = append(probeNotes, "(libvirt: listing failed)")
		}
	} else {
		probeNotes = append(probeNotes, "(libvirt: vm plugin unavailable)")
	}

	// QEMU pidfile scan
	if dir, err := vmDir(); err == nil {
		entries, derr := os.ReadDir(dir)
		if derr == nil {
			for _, entry := range entries {
				if !entry.IsDir() {
					continue
				}
				name := entry.Name()
				pidFile := filepath.Join(dir, name, "qemu.pid")
				state := "stopped"
				alive := false
				if data, err := os.ReadFile(pidFile); err == nil {
					if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
						if proc, err := os.FindProcess(pid); err == nil {
							if err := proc.Signal(syscall.Signal(0)); err == nil {
								state = "running"
								alive = true
							}
						}
					}
				}
				// Skip QEMU rows that duplicate a libvirt-listed name —
				// libvirt is authoritative when both backends know about
				// the same domain.
				duplicate := false
				for _, existing := range rows {
					if existing.Name == name {
						duplicate = true
						break
					}
				}
				if duplicate {
					continue
				}
				if !c.All && !alive {
					continue
				}
				rows = append(rows, vmRow{Name: name, Backend: "qemu", State: state})
			}
		}
	}

	if len(rows) == 0 {
		fmt.Fprintln(os.Stderr, "No VMs found")
		for _, note := range probeNotes {
			fmt.Fprintln(os.Stderr, note)
		}
		return nil
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tBACKEND\tSTATE")
	for _, r := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", r.Name, r.Backend, r.State)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	for _, note := range probeNotes {
		fmt.Fprintln(os.Stderr, note)
	}
	return nil
}

// runCleanOrphans detects orphan libvirt domains and undefines them.
// A domain is "orphan" when:
//  1. Defined in libvirt
//  2. State == shut off (not running)
//  3. Either: backing qcow2 doesn't exist, OR no matching state dir.
//
// Active (running) domains are never touched. Cleanup undefines via the vm
// plugin (clearing any managed save image libvirt wrote across a host reboot,
// which would otherwise make the domain unremovable) and removes the per-VM
// state directory.
func (c *VmListCmd) runCleanOrphans() error {
	// List + undefine orphans via the out-of-process vm plugin (go-libvirt moved there).
	raw, ok := invokeVmPlugin("list-domains", "", "")
	if !ok {
		return fmt.Errorf("vm plugin unavailable (go-libvirt is out-of-process)")
	}
	var domains []domainInfo
	if err := json.Unmarshal(raw, &domains); err != nil {
		return fmt.Errorf("listing domains: %w", err)
	}
	stateRoot, err := vmDir()
	if err != nil {
		return err
	}
	var orphans []string
	for _, d := range domains {
		if d.State == "running" {
			continue
		}
		stateDir := filepath.Join(stateRoot, d.Name)
		if _, statErr := os.Stat(stateDir); statErr == nil {
			continue // state dir present → not an orphan
		}
		orphans = append(orphans, d.Name)
	}
	if len(orphans) == 0 {
		fmt.Println("no orphan libvirt domains")
		return nil
	}
	for _, name := range orphans {
		// destroy with DeleteDisk:false → the plugin's undefine (NVRAM-aware) on a non-running orphan.
		r, rok := invokeVmPluginEnv(vmPluginEnv{VmOp: "destroy", VmName: name})
		if !rok {
			fmt.Fprintf(os.Stderr, "warning: undefine %s: vm plugin unavailable\n", name)
			continue
		}
		if e := vmPluginOpError(r); e != "" {
			fmt.Fprintf(os.Stderr, "warning: undefine %s: %s\n", name, e)
			continue
		}
		fmt.Printf("undefined orphan: %s\n", name)
	}
	return nil
}

// --- VmConsoleCmd ---

type VmConsoleCmd struct {
	Box      string `arg:"" help:"Box name"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *VmConsoleCmd) Run() error {
	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}

	backend, err := resolveVmBackend(vmConfiguredBackend(c.Box, rt.VmBackend))
	if err != nil {
		return err
	}

	name := vmName(c.Box, c.Instance)

	switch backend {
	case "libvirt":
		// Keep virsh console for interactive serial — libvirt console streams are complex
		bin, err := exec.LookPath("virsh")
		if err != nil {
			return fmt.Errorf("virsh is required for libvirt console access: %w", err)
		}
		return syscall.Exec(bin, []string{"virsh", "-c", libvirtSessionURI, "console", name}, os.Environ())

	case "qemu":
		// Pure Go unix socket relay (replaces socat)
		dir, err := vmDir()
		if err != nil {
			return err
		}
		monitorSocket := filepath.Join(dir, name, "monitor.sock")
		if _, err := os.Stat(monitorSocket); err != nil {
			return fmt.Errorf("VM %s monitor socket not found — is the VM running?", name)
		}
		return connectUnixConsole(monitorSocket)
	}
	return nil
}

// connectUnixConsole connects stdin/stdout to a unix socket in raw terminal mode.
// This replaces the socat dependency for QEMU console access.
func connectUnixConsole(socketPath string) error {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return fmt.Errorf("connecting to %s: %w", socketPath, err)
	}
	defer conn.Close() //nolint:errcheck

	// Switch terminal to raw mode
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		oldState, err := term.MakeRaw(fd)
		if err != nil {
			return fmt.Errorf("setting raw terminal mode: %w", err)
		}
		defer term.Restore(fd, oldState) //nolint:errcheck
	}

	// Bidirectional copy — relay errors are the normal "connection closed"
	// signal for an interactive console, so they're intentionally dropped.
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(conn, os.Stdin)
		close(done)
	}()
	_, _ = io.Copy(os.Stdout, conn)
	<-done
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

// --- VmSshCmd ---

type VmSshCmd struct {
	Box      string   `arg:"" help:"Box name"`
	Instance string   `short:"i" long:"instance" help:"Instance name"`
	Port     int      `short:"p" long:"port" help:"Override the host SSH port (default: resolved from the managed ssh_config alias)"`
	User     string   `short:"l" long:"user" help:"Override the SSH username (default: resolved from the managed ssh_config alias)"`
	Args     []string `arg:"" optional:"" help:"Additional SSH arguments or command"`
}

func (c *VmSshCmd) Run() error {
	sshBin, err := exec.LookPath("ssh")
	if err != nil {
		return fmt.Errorf("ssh not found: %w", err)
	}
	// Connect via the MANAGED ssh_config alias published at `charly vm create`
	// (publishVmSshAlias): it resolves the user, the HOST SSH port — INCLUDING a qemu
	// backend's AUTO-ALLOCATED port, which the removed `-p 2222` default + `@localhost`
	// could never see (the auto port lives in VmDeployState, not the vm spec) — and the
	// generated key from ~/.config/charly/ssh_config. The alias's Host stanza name IS
	// `vmName` (`charly-<box>[-<instance>]`); -l/-p explicitly override it.
	alias := vmName(c.Box, c.Instance)
	args := []string{
		"ssh",
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR",
	}
	if c.User != "" {
		args = append(args, "-l", c.User)
	}
	if c.Port != 0 {
		args = append(args, "-p", strconv.Itoa(c.Port))
	}
	args = append(args, alias)
	args = append(args, c.Args...)
	return syscall.Exec(sshBin, args, os.Environ())
}
