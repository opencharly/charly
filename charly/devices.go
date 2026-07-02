package main

import (
	"fmt"
	"os"
	"slices"
	"strings"
)

// devices.go — the KEPT core GPU/device surface after cutover C11 externalized the
// host-DETECTION LOGIC into candy/plugin-gpu. What remains here:
//   - the embedded detection DATA tables (device_patterns / gpu_vendors /
//     pci_class_labels) — kept in core because `charly doctor`'s device report reads
//     devicePatterns, so one source stays here and the Detect* shims (gpu_shim.go)
//     thread the tables into the plugin via GpuProbeInput (R3 — no duplicate copy);
//   - the pure, host-INDEPENDENT env/group helpers the deploy paths call
//     (appendAutoDetectedEnv / appendEnvUnique / appendGroupsForAMDGPU /
//     LogDetectedDevices / memlockUnlimited).
// The sysfs/exec detection PRIMITIVES (DetectGPU / DetectAMDGPU / DetectVFIO /
// DetectHostDevices / EnsureCDI / MemlockLimitBytes / VfioGroupAccessible + their
// impls + the VFIOReport/VFIOGpu/VFIOPCIDevice/DetectedDevices types) now live in
// candy/plugin-gpu; core reaches them through the resolve+Invoke shims + type aliases
// in gpu_shim.go.

// AutoDetectFlags provides --no-autodetect CLI flag via Kong.
// Embed in command structs that support device auto-detection.
type AutoDetectFlags struct {
	NoAutoDetect bool `long:"no-autodetect" help:"Disable automatic device detection"`
}

// devicePatterns lists device paths to auto-detect on the host, read from the
// device_patterns directive in the embedded charly.yml (Phase 4: data moved out of Go).
// Threaded into candy/plugin-gpu's host-devices detection via the DetectHostDevices
// shim (gpu_shim.go), and read directly by `charly doctor`'s device report (doctor.go).
// NVIDIA GPUs are handled separately via CDI/--gpus; AMD GPUs need /dev/kfd for ROCm
// compute access.
var devicePatterns = parseEmbeddedDevicePatterns()

func parseEmbeddedDevicePatterns() []string {
	var doc struct {
		DevicePatterns []string `yaml:"device_patterns"`
	}
	unmarshalEmbeddedDefaults(&doc)
	if len(doc.DevicePatterns) == 0 {
		panic("devices: embedded charly.yml has no device_patterns: directive")
	}
	return doc.DevicePatterns
}

// gpuRenderVendors is the set of PCI vendor IDs whose render node counts as a real,
// encode-capable GPU (vs the paravirtual virtio-gpu), read from the gpu_vendors directive
// in the embedded charly.yml (Phase 4: data moved out of Go). The map value is the vendor
// name (documentary); only key membership drives the plugin's render-node pick. Threaded
// into candy/plugin-gpu via the DetectHostDevices shim (gpu_shim.go).
var gpuRenderVendors = parseEmbeddedGPUVendors()

func parseEmbeddedGPUVendors() map[string]string {
	var doc struct {
		GpuVendors map[string]string `yaml:"gpu_vendors"`
	}
	unmarshalEmbeddedDefaults(&doc)
	if len(doc.GpuVendors) == 0 {
		panic("devices: embedded charly.yml has no gpu_vendors: directive")
	}
	return doc.GpuVendors
}

// pciClassLabels maps a PCI class code (high 16 bits) to a human label for VFIO
// passthrough device reporting, read from the pci_class_labels directive in the embedded
// charly.yml (Phase 4: data moved out of Go). An unknown class falls back to the raw class
// string (logic in the plugin). Threaded into candy/plugin-gpu via the DetectVFIO shim
// (gpu_shim.go).
var pciClassLabels = parseEmbeddedPCIClassLabels()

func parseEmbeddedPCIClassLabels() map[string]string {
	var doc struct {
		PciClassLabels map[string]string `yaml:"pci_class_labels"`
	}
	unmarshalEmbeddedDefaults(&doc)
	if len(doc.PciClassLabels) == 0 {
		panic("devices: embedded charly.yml has no pci_class_labels: directive")
	}
	return doc.PciClassLabels
}

// LogDetectedDevices prints detected devices to stderr.
func LogDetectedDevices(detected DetectedDevices) {
	var parts []string
	if detected.GPU {
		parts = append(parts, "NVIDIA GPU (CDI)")
	}
	if detected.AMDGPU {
		label := "AMD GPU (kfd+render)"
		if detected.AMDGFXVersion != "" {
			label = fmt.Sprintf("AMD GPU gfx %s (kfd+render)", detected.AMDGFXVersion)
		}
		parts = append(parts, label)
	}
	for _, d := range detected.Devices {
		label := d
		if d == detected.RenderNode {
			label = d + " (DRINODE)"
		}
		parts = append(parts, label)
	}
	if len(parts) > 0 {
		fmt.Fprintf(os.Stderr, "Auto-detected devices: %s\n", strings.Join(parts, ", "))
	}
}

// appendGroupsForAMDGPU adds "keep-groups" for AMD GPU access. Podman's
// keep-groups preserves all host supplementary groups (video, render, etc.)
// inside the container. It is mutually exclusive with explicit group names.
func appendGroupsForAMDGPU(groups []string) []string {
	if slices.Contains(groups, "keep-groups") {
		return groups
	}
	return appendUnique(groups, "keep-groups")
}

// appendAutoDetectedEnv injects GPU-related env vars from auto-detection results.
// Uses appendEnvUnique so user-supplied env vars always take priority.
func appendAutoDetectedEnv(envVars []string, detected DetectedDevices) []string {
	if detected.AMDGPU && detected.AMDGFXVersion != "" {
		envVars = appendEnvUnique(envVars, "HSA_OVERRIDE_GFX_VERSION="+detected.AMDGFXVersion)
	}
	if detected.RenderNode != "" {
		envVars = appendEnvUnique(envVars, "DRINODE="+detected.RenderNode)
		envVars = appendEnvUnique(envVars, "DRI_NODE="+detected.RenderNode)
	}
	return envVars
}

// appendEnvUnique appends an env var (KEY=VALUE) to a slice only if the key
// is not already present. This ensures user-supplied env vars take priority.
func appendEnvUnique(envVars []string, kv string) []string {
	key := strings.SplitN(kv, "=", 2)[0] + "="
	for _, e := range envVars {
		if strings.HasPrefix(e, key) {
			return envVars // key already set, don't override
		}
	}
	return append(envVars, kv)
}

// memlockUnlimited reports whether the hard limit is effectively unlimited. VFIO
// passthrough pins all guest RAM (see the MemlockLimitBytes shim in gpu_shim.go);
// consumed by `charly vm gpu status` + `charly doctor`.
func memlockUnlimited(hard uint64) bool { return hard >= 1<<62 }
