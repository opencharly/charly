package vm

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// consoleHint returns the `charly vm console` argument string that attaches to
// THIS domain: the bare entity for a direct create (domainID == entity), else
// `<entity> --domain <domainID>` for a per-deploy domain (P33). Passing
// `--domain <domainID>` reconstructs exactly `charly-<domainID>`, so the hint
// targets this deploy's own domain — never the shared entity's (which, for a bed,
// is a domain that does not exist).
func consoleHint(entity, domainID string) string {
	if domainID == entity {
		return entity
	}
	return entity + " --domain " + domainID
}

// runVmSpecCreateLibvirt creates the VM via the libvirt backend: render the
// domain XML, define+start it, apply autostart + raw snippets, and publish the
// managed ssh-config alias.
func runVmSpecCreateLibvirt(spec *VmSpec, rt VmRuntimeParams, vmDomainName, home, vmName, name string) error {
	xmlStr, err := RenderDomainXML(spec, rt)
	if err != nil {
		return fmt.Errorf("rendering domain XML for %s: %w", vmDomainName, err)
	}
	conn, err := connectLibvirt("")
	if err != nil {
		return fmt.Errorf("connecting to libvirt: %w", err)
	}
	defer conn.Close() //nolint:errcheck
	if err := conn.defineAndStartDomain(xmlStr, vmDomainName); err != nil {
		return fmt.Errorf("creating VM %s: %w", vmDomainName, err)
	}
	fmt.Fprintf(os.Stderr, "Created VM %s (libvirt session)\n", vmDomainName)

	// Boot-autostart: set the libvirt flag, then wire the session-boot
	// trigger (linger + user socket). The flag is load-bearing; the
	// prereqs are best-effort with actionable warnings on failure.
	if spec.Autostart {
		if err := conn.setDomainAutostart(vmDomainName, true); err != nil {
			return fmt.Errorf("enabling autostart for %s: %w", vmDomainName, err)
		}
		fmt.Fprintf(os.Stderr, "Autostart enabled for %s\n", vmDomainName)
		// HOST-PASSES-DATA: the boot-autostart prereqs (systemd linger + the user boot unit) are
		// a HOST-side systemd concern; the host applies them after the create RPC returns.
	}

	// Inject any raw libvirt snippets from candy/spec.libvirt.snippets.
	if spec.Libvirt != nil && len(spec.Libvirt.Snippets) > 0 {
		if err := InjectLibvirtXML(vmDomainName, spec.Libvirt.Snippets); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: libvirt snippet injection: %v\n", err)
		}
	}
	// HOST-PASSES-DATA: publishing the managed ssh-config alias is a host-side ~/.config concern;
	// the host writes it (VmSshStanza / EnsureSshConfigInclude) after the create RPC.
	fmt.Fprintf(os.Stderr, "Console: charly vm console %s\n", consoleHint(vmName, name))
	return nil
}

// runVmSpecCreateQemu creates the VM via the direct-QEMU backend: render argv,
// persist the relaunch command line, run qemu-system, and publish the managed
// ssh-config alias.
func runVmSpecCreateQemu(spec *VmSpec, rt VmRuntimeParams, vmDomainName, home, vmName, name, vmStateDir string) error {
	if spec.Libvirt != nil && len(spec.Libvirt.Snippets) > 0 {
		fmt.Fprintf(os.Stderr, "Warning: libvirt snippets are not supported with the QEMU backend (skipping %d snippet(s))\n", len(spec.Libvirt.Snippets))
	}

	// Prepare QEMU runtime paths.
	monitorSocket := filepath.Join(vmStateDir, "monitor.sock")
	qmpSocket := filepath.Join(vmStateDir, "qmp.sock")
	consoleSocket := filepath.Join(vmStateDir, "console.sock")
	pidFile := filepath.Join(vmStateDir, "qemu.pid")
	qemuPaths := QemuRuntimePaths{
		MonitorSocket: monitorSocket,
		QmpSocket:     qmpSocket,
		ConsoleSocket: consoleSocket,
		PidFile:       pidFile,
	}

	args := RenderQemuArgv(spec, rt, qemuPaths)
	bin := qemuSystemBinary()

	// Persist the command line so `charly vm start <name>` can relaunch.
	cmdLine := bin + " " + strings.Join(args, " ")
	if err := os.WriteFile(filepath.Join(vmStateDir, "command"), []byte(cmdLine), 0o644); err != nil {
		return err
	}

	cmd := exec.Command(bin, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("qemu failed: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Created and started VM %s (QEMU)\n", vmDomainName)
	// HOST-PASSES-DATA: publishing the managed ssh-config alias is a host-side ~/.config concern;
	// the host writes it (VmSshStanza / EnsureSshConfigInclude) after the create RPC.
	fmt.Fprintf(os.Stderr, "Console: charly vm console %s\n", consoleHint(vmName, name))
	return nil
}

// publishVmSshAlias (the managed ssh-config alias + per-VM known_hosts refresh) moved HOST-side:
// it manages the operator's ~/.config/charly/ssh_config + ~/.ssh/config — a host concern the
// out-of-process plugin must not touch. The host runs it after the create RPC returns,
// reusing the VmSshStanza / WriteVmSshStanza / EnsureSshConfigInclude that stay in core.
