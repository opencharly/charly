package spec

import (
	"errors"
	"strings"
)

// gpu_select.go — the pure GPU selection helpers + the driver-switch wedge sentinel,
// shared between charly's core (auto-allocation gpu_allocate.go, vm_gpu_cmd.go) and the
// DRIVER-SWITCH logic now in candy/plugin-gpu (cutover C9). Both need them; a single copy
// in package spec (aliased in core) keeps them from drifting (R3).

// ErrGPUSwitchWedged signals that a GPU driver switch did not complete because the nvidia
// `.remove` is stuck holding the device_lock (deadline exceeded, or a self-detected D-state
// task in nv_pci_remove). candy/plugin-gpu carries the wedge as a bool over the wire
// (GpuSwitchReply.Wedged); the core gpu shim maps that bool back to this sentinel so callers
// (vm_gpu_cmd) keep matching it with errors.Is. Recovery is a host reboot.
var ErrGPUSwitchWedged = errors.New("GPU driver switch wedged: nvidia .remove stuck holding the device_lock — host reboot required")

// NormalizePCIVendor lowercases a PCI vendor id and ensures the "0x" prefix, so "10DE" /
// "0X10de" / "0x10de" all compare equal to the sysfs form ("0x10de").
func NormalizePCIVendor(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ""
	}
	if !strings.HasPrefix(s, "0x") {
		s = "0x" + s
	}
	return s
}

// SelectGPUByVendor returns the first passthrough-capable GPU whose PCI vendor matches
// (case/prefix-insensitive). ok=false when no GPU matches.
func SelectGPUByVendor(rep VFIOReport, vendor string) (VFIOGpu, bool) {
	want := NormalizePCIVendor(vendor)
	for _, g := range rep.GPUs {
		if NormalizePCIVendor(g.VendorID) == want {
			return g, true
		}
	}
	return VFIOGpu{}, false
}
