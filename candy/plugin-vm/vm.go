package vm

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

// domainOr returns the per-deploy DOMAIN IDENTITY when the `--domain` flag is set (the deploy path,
// where the libvirt domain is keyed by the DEPLOY name so sibling beds sharing one kind:vm entity get
// distinct, collision-free domains), else the positional box/entity (the direct `charly vm …` path,
// whose domain identity IS the entity — behavior unchanged). The positional arg still drives entity
// spec/backend/claimant resolution; only the domain NAME + ssh alias + per-domain state switch to it.
func domainOr(box, domain string) string {
	if domain != "" {
		return domain
	}
	return box
}

// vmDir returns the directory for storing VM state (QEMU backend).
func vmDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "charly", "vm"), nil
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
	Domain          string `long:"domain" help:"Per-deploy domain identity: name the libvirt domain charly-<domain> (+ its per-domain disk overlay/state/ssh alias) after the DEPLOY, not the kind:vm entity. Set by the deploy path so sibling beds sharing one entity get distinct domains; absent for a direct create (domain = entity)."`
	SshKey          string `long:"ssh-key" default:"auto" help:"SSH public key: path to .pub file, 'auto' (default ~/.ssh key), 'generate', or 'none'"`
	AutoDetectFlags `embed:""`
}

func (c *VmCreateCmd) Run() error {
	// The host resolves the kind:vm entity + backend + resources + claimant + runtime settings over
	// the config-resolve seam (the loader / settings store / backend probe are core Mechanisms the
	// plugin cannot hold). resolveVmBackend inside the seam also starts the libvirt user session
	// before returning reply.Backend, so no separate startLibvirtUserSession is needed here. The
	// entity's `backend:` pin is honored host-side (vmConfiguredBackend runs before the probe).
	reply, err := hostConfigResolve(c.Box)
	if err != nil {
		return err
	}

	// Resource arbitration: a standalone `charly vm create` of a VM that a deploy/check node claims
	// via requires_exclusive preempts the running holders of that resource (persistent lease — released
	// by `charly vm stop`/`destroy`). No-op when no claimant node references this entity
	// (reply.ClaimantNode nil) or an outer orchestrator already owns the lease (CHARLY_PREEMPT_LEASE).
	if reply.ClaimantNode != nil {
		if _, perr := acquireExclusiveForClaimant(reply.Claimant, *reply.ClaimantNode, false); perr != nil {
			return perr
		}
	}

	if reply.VM != nil {
		// VmSpec-driven create pipeline: RenderDomain for libvirt, RenderQemuArgv for qemu. Uses
		// output/qcow2/{disk,seed} produced by `charly vm build`. claimantNode + resources drive GPU
		// auto-allocation (gpu_allocate.go).
		return c.runVmSpecCreate(c.Box, reply.VM, reply.Backend, reply.ClaimantNode, reply.Resources, reply.VmState)
	}

	// Reached here = image is not a `kind: vm` entity.
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
	Domain   string `long:"domain" help:"Per-deploy domain identity (start charly-<domain>, keyed by the DEPLOY not the entity); absent for a direct start (domain = entity)."`
}

func (c *VmStartCmd) Run() error {
	return startVM(c.Box, c.Instance, c.Domain)
}

// startVM starts a previously-created VM by image+instance, dispatching by
// backend (libvirt domain start / re-exec the stored qemu command). Shared
// by VmStartCmd.Run and the resource arbiter (charly/preempt.go) so the holder-
// restart path runs the exact same lifecycle code as `charly vm start`.
func startVM(box, instance, domain string) error {
	reply, err := hostConfigResolve(box)
	if err != nil {
		return err
	}
	backend := reply.Backend

	name := vmName(domainOr(box, domain), instance)

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
	Domain   string `long:"domain" help:"Per-deploy domain identity (stop charly-<domain>, keyed by the DEPLOY not the entity); absent for a direct stop (domain = entity)."`
	Force    bool   `long:"force" help:"Force stop (destroy) instead of graceful shutdown"`
}

func (c *VmStopCmd) Run() error {
	if err := stopVM(c.Box, c.Instance, c.Domain, c.Force); err != nil {
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
func stopVM(box, instance, domain string, force bool) error {
	reply, err := hostConfigResolve(box)
	if err != nil {
		return err
	}
	backend := reply.Backend

	name := vmName(domainOr(box, domain), instance)

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
	Domain     string `long:"domain" help:"Per-deploy domain identity (destroy charly-<domain>, keyed by the DEPLOY not the entity); absent for a direct destroy (domain = entity)."`
	Disk       bool   `long:"disk" help:"Also delete the QCOW2 disk image"`
	KeepDeploy bool   `long:"keep-deploy" help:"Keep the charly.yml vm:<name> entry (default: remove it, like 'charly remove' for pods)"`
}

// destroyVmDomain tears the VM domain named `name` down from whichever backend ACTUALLY holds it
// and VERIFIES the teardown, returning torn=true only when a domain was found and removed. It probes
// libvirt authoritatively — the domain-state op spawns the libvirt session daemon (connectLibvirt),
// so a libvirt domain is detected even when the configured/default backend is qemu; qemu is a
// state-dir probe. torn=false with a nil error means no domain exists in EITHER backend (the caller
// decides whether that is an error). This replaces the old "trust the single resolved backend" arm,
// which took the qemu no-op path for a libvirt domain and falsely reported "Destroyed VM" (#69).
func destroyVmDomain(name string, deleteDisk bool) (bool, error) {
	// libvirt first — the authoritative existence probe (domain-state spawns the session daemon).
	if raw, ok := invokeVmPluginEnv(vmPluginEnv{VmOp: "domain-state", VmName: name}); ok && vmPluginOpFlag(raw, "exists") {
		dr, ok := invokeVmPluginEnv(vmPluginEnv{VmOp: "destroy", VmName: name, DeleteDisk: deleteDisk})
		if !ok {
			return false, fmt.Errorf("VM %s: vm plugin unavailable (go-libvirt is out-of-process)", name)
		}
		if e := vmPluginOpError(dr); e != "" {
			return false, fmt.Errorf("destroying VM %s: %s", name, e)
		}
		// Confirm the domain is really gone: undefining a still-running domain that was NOT
		// force-stopped first leaves a lingering TRANSIENT domain — the exact false-success this
		// closes — so a residual definition is a real failure, never a silent success.
		if vr, ok := invokeVmPluginEnv(vmPluginEnv{VmOp: "domain-state", VmName: name}); ok && vmPluginOpFlag(vr, "exists") {
			return false, fmt.Errorf("VM %s: libvirt reported the destroy succeeded but the domain is still defined", name)
		}
		return true, nil
	}

	// qemu — a per-VM state dir with a (possibly running) qemu process.
	dir, err := vmDir()
	if err != nil {
		return false, err
	}
	stateDir := filepath.Join(dir, name)
	if _, statErr := os.Stat(stateDir); statErr != nil {
		return false, nil // no libvirt domain and no qemu state dir → nothing to destroy
	}
	// Kill process — try QMP quit first, fall back to PID kill.
	if err := qemuForceShutdown(stateDir); err != nil {
		killQemuByPID(stateDir)
	}
	if err := os.RemoveAll(stateDir); err != nil {
		return false, fmt.Errorf("removing qemu state dir %s: %w", stateDir, err)
	}
	return true, nil
}

func (c *VmDestroyCmd) Run() error {
	// Releasing a persistent exclusive claim on this VM restores any preempted
	// holder once the claimant is gone (deferred so it runs on every exit;
	// no-op if no lease / gated by an outer orchestrator).
	if claimant, _, ok := lookupVMClaimant(c.Box); ok {
		defer releaseResourceClaim(claimant)
	}

	// The libvirt domain + per-domain state + ssh alias are keyed by the DEPLOY (domain identity),
	// so a deploy's destroy targets charly-<domain> and never a sibling bed sharing the entity.
	name := vmName(domainOr(c.Box, c.Domain), c.Instance)

	// Tear the domain down from whichever backend ACTUALLY holds it, verifying it is gone — never
	// trust a resolved/default backend. A deploy-name positional resolves no entity `backend:` pin,
	// so the backend fell back to the global vm.backend setting; when that was qemu but the live
	// domain was libvirt, the old code took the qemu no-op arm and printed "Destroyed VM" while the
	// libvirt domain kept running (#69).
	torn, err := destroyVmDomain(name, c.Disk)
	if err != nil {
		return err
	}
	if !torn {
		// No domain in EITHER backend — a genuinely-absent target must FAIL, never report a false
		// "Destroyed VM" success (#69 regression: destroy-nonexistent exits non-zero).
		return fmt.Errorf("no such VM %q: no libvirt domain and no qemu state — nothing destroyed", name)
	}
	fmt.Fprintf(os.Stderr, "Destroyed VM %s\n", name)

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
		// Key the removed entry by the DOMAIN IDENTITY (vm:<domain>) — that is where the port +
		// instance-id ledger for THIS domain lives (runVmSpecCreate persists vm:<domain>). Removing
		// vm:<entity> instead would, via removeVmDeployEntry's From-scan, over-match every sibling
		// bed sharing the entity — the collision this cutover eliminates.
		deployName := "vm:" + deployKey(domainOr(c.Box, c.Domain), c.Instance)
		if err := hostConfigPersist(deployName, "", nil, true); err != nil {
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
	_, _ = fmt.Fprintln(tw, "NAME\tBACKEND\tSTATE")
	for _, r := range rows {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", r.Name, r.Backend, r.State)
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
	Domain   string `long:"domain" help:"Per-deploy domain identity (console charly-<domain>, keyed by the DEPLOY not the entity); absent for a direct console (domain = entity)."`
}

func (c *VmConsoleCmd) Run() error {
	reply, err := hostConfigResolve(c.Box)
	if err != nil {
		return err
	}
	backend := reply.Backend

	name := vmName(domainOr(c.Box, c.Domain), c.Instance)

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
