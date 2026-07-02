package vmshared

// Helpers shared by the libvirt YAML bridge + qemu_render argv emitter.
// Moved here from the old libvirt_render.go as part of the libvirtxml
// cutover, when the rest of that file was deleted.

import (
	"strings"
)

// VmRuntimeParams carries the runtime-resolved state that the
// libvirt-XML and QEMU-argv emitters need but isn't in the author's
// VmSpec: the VM name, disk paths, SSH pubkey, host architecture,
// host CPU vendor, etc. Both RenderDomainXML (libvirt) and
// RenderQEMUArgs (qemu) consume the same struct so the "rendered
// from a common source" invariant is preserved.
type VmRuntimeParams struct {
	// Name is the libvirt domain name / QEMU process handle.
	Name string

	// QCOW2Path is the absolute path to the VM's root qcow2 disk.
	QCOW2Path string

	// SeedISOPath is the absolute path to the NoCloud cidata ISO.
	// Empty → no cdrom attached (bootc source with cloud-init disabled).
	SeedISOPath string

	// NVRAMPath is the absolute path to the per-VM UEFI NVRAM file.
	// Empty → firmware: bios (no pflash drives emitted).
	NVRAMPath string

	// OVMFCodePath is the absolute path to the OVMF_CODE firmware image.
	// Required when Firmware == "uefi-*"; empty when bios.
	OVMFCodePath string

	// HostArch is the host architecture string (e.g. "x86_64", "aarch64").
	HostArch string

	// HostCPUVendor is "GenuineIntel" | "AuthenticAMD" | "". Used to
	// auto-append +vmx or +svm in resolveCPUDefaults when mode defaults
	// to host-passthrough and the user hasn't explicitly disabled nested
	// virt.
	HostCPUVendor string

	// SMBIOSCredentials are pre-formatted systemd-credential oemString
	// entries (e.g. "io.systemd.credential.binary:tmpfiles.extra=<b64>").
	SMBIOSCredentials []string

	// RamMB is the resolved RAM in MiB (VmSpec.Ram parsed).
	RamMB int

	// Cpus is the resolved vCPU count (VmSpec.Cpus with defaults applied).
	Cpus int

	// SshPort is the host port forwarded to guest :22.
	SshPort int

	// ExtraPortForwards are additional "host:guest" TCP forwards on
	// top of the SSH port. Used with user-mode networking.
	ExtraPortForwards []string

	// VmStateDir is the absolute path to the per-VM state directory
	// (~/.local/share/charly/vm/charly-<name>/). Used by the libvirt YAML
	// bridge to expand `{{.VmStateDir}}` template references in
	// path-bearing libvirt attributes (channel <source path=>,
	// graphics socket paths). Populated by the create-time caller
	// (vm_create_spec.go::runVmSpecCreate) so author-supplied paths
	// stay portable across users without hardcoded /home/<x>.
	VmStateDir string
}

// boolPtrTrue returns true when p is non-nil and *p is true.
func boolPtrTrue(p *bool) bool { return p != nil && *p }

// boolPtrDefaultTrue returns true when p is nil (default enabled) OR *p is true.
func boolPtrDefaultTrue(p *bool) bool { return p == nil || *p }

// boolPtrToYesNo maps a *bool to libvirt's "yes"/"no" attribute string.
// Nil → "no" matches the legacy renderer's behavior.
func boolPtrToYesNo(p *bool) string {
	if p == nil || !*p {
		return "no"
	}
	return "yes"
}

// defaultMachineForArch returns the canonical libvirt machine type for
// an architecture when spec.Machine is empty.
func defaultMachineForArch(arch string) string {
	switch arch {
	case "x86_64", "amd64":
		return "q35"
	case "aarch64", "arm64":
		return "virt"
	case "riscv64":
		return "virt"
	default:
		return "q35"
	}
}

// resolveCPUDefaults applies the D16 defaults: mode=host-passthrough,
// check=none, auto-append +vmx (Intel) or +svm (AMD) when nested virt
// is possible and not explicitly disabled. Returns a COPY so callers
// don't mutate the spec.
func resolveCPUDefaults(spec *VmSpec, rt VmRuntimeParams) LibvirtCPU {
	var src LibvirtCPU
	if spec.Libvirt != nil && spec.Libvirt.CPU != nil {
		src = *spec.Libvirt.CPU
	}
	if src.Mode == "" {
		src.Mode = "host-passthrough"
	}
	if src.Check == "" {
		src.Check = "none"
	}
	if src.Mode == "host-passthrough" {
		nested := nestedFeatureForVendor(rt.HostCPUVendor)
		if nested != "" && !hasCPUFeature(src.Features, nested) {
			src.Features = append(src.Features, LibvirtCPUFeature{
				Policy: "require",
				Name:   nested,
			})
		}
	}
	return src
}

// nestedFeatureForVendor returns "vmx" on Intel, "svm" on AMD.
func nestedFeatureForVendor(vendor string) string {
	switch vendor {
	case "GenuineIntel":
		return "vmx"
	case "AuthenticAMD":
		return "svm"
	}
	return ""
}

// hasCPUFeature checks whether a feature name appears in the list.
func hasCPUFeature(features []LibvirtCPUFeature, name string) bool {
	for _, f := range features {
		if f.Name == name {
			return true
		}
	}
	return false
}

// splitPortForward splits a "host:guest" port-forward string into two
// parts. Returns "", "" if malformed.
func splitPortForward(pf string) (host, guest string) {
	parts := strings.SplitN(pf, ":", 2)
	if len(parts) != 2 {
		return "", ""
	}
	return parts[0], parts[1]
}
