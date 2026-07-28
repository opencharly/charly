package main

import (
	"os/exec"
)

// vm_backend_lifecycle.go — startLibvirtUserSession: best-effort libvirt user-session daemon
// spawn. Called directly by core residue files that pre-warm it before a VM/group-bed probe
// (bundle_members.go, check_bed_run.go) — unrelated to the config-resolve seam. candy/plugin-vm
// carries its OWN copy (vm_phaseA_shims.go, R3 — a separate module) for its in-process VM-backend
// probe (vm_backend_resolve.go); this core copy stays because those 2 core callers still need it
// in-process and are outside this cutover's scope (coneA's vm-deploy-lifecycle domain) — vacating
// this file fully would require also moving THOSE 2 call sites, which is cross-cone (flagged to
// team-lead rather than touched unilaterally).
//
// EVERYTHING ELSE this file used to carry is now GONE (F6 vm-lifecycle move, coneB-vmlifecycle):
//   - resolveVmBackend/vmConfiguredBackend MOVED to candy/plugin-vm/vm_backend_resolve.go — a pure
//     host-env probe with zero core-registry coupling, its one LoadUnified-coupled dependency (the
//     entity's `backend:` pin) already reachable via the generic "deploy-entity-resolve" seam every
//     other F6 consumer uses.
//   - resolveSSHPubKey/containerSSHKeyDir/generateSSHKeypair HOISTED to sdk/sshx (R3 dedup): they
//     were byte-identically duplicated in this file AND candy/plugin-vm (vm.go) for a
//     coincidentally-identical but independent need (pod container SSH keys vs VM cloud-init SSH
//     keys); host_build_pod_config_seams.go (the ONLY real core caller — NOT a VM concern despite
//     this file's former misattributed header, R1) now calls sshx.ResolveSSHPubKey/
//     sshx.ContainerSSHKeyDir directly.
//   - The FORMER resource-arbiter consumer (preempt.go's startVM/stopVM/vmName holder start/stop)
//     moved into candy/plugin-preempt (FLOOR-SLIM-proper Unit-8) with its own vmName/start/stop
//     implementation dispatching verb:libvirt directly via InvokeProvider — so those symbols, and
//     their qemu-backend-only support cluster (vm_qemu_client.go, vm_plugin_client.go's op-reply
//     decoders, vmshared_aliases.go's killQemuByPID), are DELETED here (R5). `vmDir` (K3 vm-build
//     move, coneB-buildremnant) is ALSO already deleted.

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
// Package-level var (not a plain func) so a hermetic test could stub it to a
// no-op if needed — matching candy/plugin-vm's own copy (vm_phaseA_shims.go),
// whose OWN test suite stubs it this way (stubNoLibvirtSpawn,
// vm_backend_resolve_test.go) for its resolveVmBackendPlugin coverage.
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
