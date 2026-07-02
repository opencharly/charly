package vmshared

import (
	"fmt"
	"strconv"
	"strings"
)

// QemuRuntimePaths carries backend-specific paths that QEMU needs but
// libvirt doesn't (socket paths, pidfile). Composed with VmRuntimeParams
// when the QEMU backend renders its argv.
type QemuRuntimePaths struct {
	// MonitorSocket — unix socket for the QEMU monitor (`-monitor`).
	MonitorSocket string

	// QmpSocket — unix socket for QMP (`-qmp`).
	QmpSocket string

	// ConsoleSocket — unix socket for the serial console (`-serial`).
	ConsoleSocket string

	// PidFile — `-pidfile` target.
	PidFile string
}

// RenderQemuArgv produces the full argv for `qemu-system-<arch>` from a
// VmSpec + VmRuntimeParams + QemuRuntimePaths. Pure function — no
// filesystem or process state side effects.
//
// Covers the intersection of libvirt schema features that map cleanly
// to QEMU: machine, cpu (D16 defaults), firmware (D17 pflash), disks
// (root + seed iso D5 + additional), network (user-mode hostfwd),
// SMBIOS credentials (D13), RNG, balloon, qemu-guest-agent channel.
//
// Structured libvirt features without a QEMU mapping (virtiofs,
// launch_security beyond SEV, PCI hostdev, graphics/spice, TPM) are
// skipped with a warning comment inserted via the caller's log output —
// this function only returns argv.
func RenderQemuArgv(spec *VmSpec, rt VmRuntimeParams, paths QemuRuntimePaths) []string {
	arch := rt.HostArch
	if arch == "" {
		arch = "x86_64"
	}
	machine := spec.Machine
	if machine == "" {
		machine = defaultMachineForArch(arch)
	}

	var args []string

	// --- Machine, firmware (D17), memory, vCPUs, CPU (D16) ---

	machineArg := machine
	if spec.Firmware == "uefi-secure" {
		// SMM is required for secure boot. libvirt's schema puts
		// `smm='on'` under <features>; QEMU puts it in `-machine`.
		machineArg += ",smm=on"
	}
	args = append(args, "-machine", machineArg)

	args = append(args, "-m", strconv.Itoa(rt.RamMB))
	args = append(args, "-smp", strconv.Itoa(rt.Cpus))

	args = append(args, "-cpu", resolveQemuCPUArg(spec, rt))

	args = append(args, "-enable-kvm")

	// --- UEFI firmware pflash (D17) ---

	if spec.Firmware == "uefi-insecure" || spec.Firmware == "uefi-secure" {
		if rt.OVMFCodePath != "" {
			readOnly := "readonly=on"
			secure := ""
			if spec.Firmware == "uefi-secure" {
				secure = ",secure=on"
			}
			args = append(args, "-drive",
				fmt.Sprintf("if=pflash,format=raw,%s,file=%s%s",
					readOnly, rt.OVMFCodePath, secure))
		}
		if rt.NVRAMPath != "" {
			args = append(args, "-drive",
				fmt.Sprintf("if=pflash,format=raw,file=%s", rt.NVRAMPath))
		}
	}

	// --- Root qcow2 disk ---

	if rt.QCOW2Path != "" {
		args = append(args, "-drive",
			fmt.Sprintf("file=%s,format=qcow2,if=virtio", rt.QCOW2Path))
	}

	// --- Seed ISO cdrom (D5) ---

	if rt.SeedISOPath != "" {
		args = append(args, "-drive",
			fmt.Sprintf("file=%s,media=cdrom,readonly=on", rt.SeedISOPath))
	}

	// --- Additional disks from structured config ---

	if spec.Libvirt != nil && spec.Libvirt.Devices != nil {
		for _, d := range spec.Libvirt.Devices.Disks {
			if arg := qemuDriveArg(d); arg != "" {
				args = append(args, "-drive", arg)
			}
		}
	}

	// --- Network (user-mode with hostfwd port forwarding) ---

	args = append(args, renderQemuNic(spec, rt)...)

	// --- SMBIOS credentials (D13 SSH-key-via-SMBIOS) ---

	for _, cred := range rt.SMBIOSCredentials {
		args = append(args, "-smbios", fmt.Sprintf("type=11,value=%s", cred))
	}

	// --- RNG, balloon, qemu-guest-agent (from structured config) ---

	if spec.Libvirt != nil && spec.Libvirt.Devices != nil {
		d := spec.Libvirt.Devices
		for _, r := range d.RNG {
			args = append(args, qemuRNGArgs(r)...)
		}
		if d.MemBalloon != nil && d.MemBalloon.Model == "virtio" {
			args = append(args, "-device", "virtio-balloon-pci")
		}
		for _, ch := range d.Channels {
			if ch.Name == "org.qemu.guest_agent.0" {
				args = append(args, qemuGuestAgentArgs(rt)...)
			}
		}
	}

	// --- Monitor / QMP / serial console sockets ---

	if paths.MonitorSocket != "" {
		args = append(args, "-monitor",
			fmt.Sprintf("unix:%s,server,nowait", paths.MonitorSocket))
	}
	if paths.QmpSocket != "" {
		args = append(args, "-qmp",
			fmt.Sprintf("unix:%s,server,nowait", paths.QmpSocket))
	}
	if paths.ConsoleSocket != "" {
		args = append(args, "-serial",
			fmt.Sprintf("unix:%s,server,nowait", paths.ConsoleSocket))
	}

	args = append(args, "-display", "none", "-daemonize")

	if paths.PidFile != "" {
		args = append(args, "-pidfile", paths.PidFile)
	}

	return args
}

// resolveQemuCPUArg produces the `-cpu <value>` arg, applying D16
// defaults (host-passthrough + nested-virt feature).
func resolveQemuCPUArg(spec *VmSpec, rt VmRuntimeParams) string {
	mode := "host-passthrough"
	var model string
	var features []LibvirtCPUFeature

	if spec.Libvirt != nil && spec.Libvirt.CPU != nil {
		cpu := spec.Libvirt.CPU
		if cpu.Mode != "" {
			mode = cpu.Mode
		}
		if cpu.Model != "" {
			model = cpu.Model
		}
		features = append(features, cpu.Features...)
	}

	// QEMU spelling differs from libvirt:
	//   libvirt host-passthrough → qemu "host"
	//   libvirt host-model       → qemu "host-model" (via -cpu max in practice,
	//                               but keeping the verbatim name works on
	//                               modern QEMU with recent machine types)
	//   libvirt custom           → qemu <model>
	base := "host"
	switch mode {
	case "host-passthrough":
		base = "host"
	case "host-model":
		base = "max"
	case "custom":
		if model != "" {
			base = model
		}
	}

	// D16 nested-virt: auto-append +vmx/+svm when base == "host"
	// (i.e. user hasn't pinned a model) and the host vendor is
	// detectable, unless user explicitly disabled.
	if base == "host" {
		nested := nestedFeatureForVendor(rt.HostCPUVendor)
		if nested != "" && !hasDisabledFeature(features, nested) && !hasRequiredFeature(features, nested) {
			features = append(features, LibvirtCPUFeature{Policy: "require", Name: nested})
		}
	}

	if len(features) == 0 {
		return base
	}
	parts := []string{base}
	for _, f := range features {
		switch f.Policy {
		case "disable", "forbid":
			parts = append(parts, "-"+f.Name)
		default:
			parts = append(parts, "+"+f.Name)
		}
	}
	return strings.Join(parts, ",")
}

func hasDisabledFeature(features []LibvirtCPUFeature, name string) bool {
	for _, f := range features {
		if f.Name == name && (f.Policy == "disable" || f.Policy == "forbid") {
			return true
		}
	}
	return false
}

func hasRequiredFeature(features []LibvirtCPUFeature, name string) bool {
	for _, f := range features {
		if f.Name == name && (f.Policy == "require" || f.Policy == "force" || f.Policy == "") {
			return true
		}
	}
	return false
}

// qemuDriveArg translates a structured LibvirtDisk to a `-drive` arg.
// Returns "" when the disk shape isn't QEMU-expressible.
func qemuDriveArg(d LibvirtDisk) string {
	file, hasFile := d.Source["file"]
	if !hasFile {
		return ""
	}
	format := "qcow2"
	if d.Driver != nil {
		if t, ok := d.Driver["type"]; ok {
			format = t
		}
	}
	iface := "virtio"
	if d.Target != nil {
		if bus, ok := d.Target["bus"]; ok {
			iface = bus
		}
	}
	parts := []string{
		fmt.Sprintf("file=%s", file),
		fmt.Sprintf("format=%s", format),
		fmt.Sprintf("if=%s", iface),
	}
	if d.Device == "cdrom" {
		parts = append(parts, "media=cdrom")
	}
	if boolPtrTrue(d.Readonly) {
		parts = append(parts, "readonly=on")
	}
	return strings.Join(parts, ",")
}

// renderQemuNic produces the `-nic` arg(s) from VmSpec.Network + the
// SSH hostfwd from RuntimeParams. user-mode is fully supported;
// bridge/nat mode requires host privileges so we emit the arg but the
// caller's QEMU may fail without CAP_NET_ADMIN.
func renderQemuNic(spec *VmSpec, rt VmRuntimeParams) []string {
	net := &VmNetwork{Mode: "user", Model: "virtio-net-pci"}
	if spec.Network != nil {
		net = spec.Network
		if net.Mode == "" {
			net.Mode = "user"
		}
		if net.Model == "" {
			net.Model = "virtio-net-pci"
		}
	}

	switch net.Mode {
	case "bridge":
		arg := fmt.Sprintf("bridge,br=%s,model=%s", net.Bridge, net.Model)
		if net.MAC != "" {
			arg += ",mac=" + net.MAC
		}
		return []string{"-nic", arg}
	case "nat", "network":
		// QEMU doesn't have a first-class nat; fall through to user.
		fallthrough
	default:
		fwds := []string{fmt.Sprintf("hostfwd=tcp::%d-:22", rt.SshPort)}
		for _, pf := range net.PortForwards {
			host, guest := splitPortForward(pf)
			if host != "" && guest != "" {
				fwds = append(fwds, fmt.Sprintf("hostfwd=tcp::%s-:%s", host, guest))
			}
		}
		for _, pf := range rt.ExtraPortForwards {
			host, guest := splitPortForward(pf)
			if host != "" && guest != "" {
				fwds = append(fwds, fmt.Sprintf("hostfwd=tcp::%s-:%s", host, guest))
			}
		}
		arg := fmt.Sprintf("user,model=%s,%s", net.Model, strings.Join(fwds, ","))
		if net.MAC != "" {
			arg += ",mac=" + net.MAC
		}
		return []string{"-nic", arg}
	}
}

// qemuRNGArgs produces the -object/-device pair for a virtio-rng device.
func qemuRNGArgs(r LibvirtRNG) []string {
	backend := r.Backend
	if backend == "" {
		backend = "/dev/urandom"
	}
	// Standard QEMU idiom: object rng-random + device virtio-rng-pci.
	return []string{
		"-object", fmt.Sprintf("rng-random,id=rng0,filename=%s", backend),
		"-device", "virtio-rng-pci,rng=rng0",
	}
}

// qemuGuestAgentArgs wires the virtio-serial channel that the
// qemu-guest-agent uses. The agent itself runs inside the guest.
func qemuGuestAgentArgs(rt VmRuntimeParams) []string {
	sockPath := fmt.Sprintf("/tmp/charly-qga-%s.sock", rt.Name)
	return []string{
		"-chardev", fmt.Sprintf("socket,path=%s,server=on,wait=off,id=qga0", sockPath),
		"-device", "virtio-serial",
		"-device", "virtserialport,chardev=qga0,name=org.qemu.guest_agent.0",
	}
}
