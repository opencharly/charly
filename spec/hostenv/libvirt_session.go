package hostenv

import "os/exec"

// libvirt_session.go — StartLibvirtUserSession, RELOCATED from sdk/vmshared (#55 vmshared Bucket C):
// a pure host-environment action with zero registry/loader coupling, so it belongs in this
// kind-agnostic host-probe leaf, shared by every caller — sdk/deploykit.BringUpMembers (#55 W3
// A4), candy/plugin-check's bed_session.go (#55 W3 B2-full — both pre-warm it before a VM/group-bed
// probe) and candy/plugin-vm (vm_libvirt.go, vm_backend_resolve.go, reaching it via the sdk/vmshared
// re-export forwarder).

// StartLibvirtUserSession ensures the libvirt user-session daemon is
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
// Package-level var (not a plain func) so a caller's test can stub it to a
// no-op when needed (e.g. candy/plugin-vm's resolveVmBackendPlugin coverage,
// vm_backend_resolve_test.go's stubNoLibvirtSpawn, which stubs the sdk/vmshared
// re-export forwarder it also calls — self-consistent within that package).
var StartLibvirtUserSession = func() {
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
