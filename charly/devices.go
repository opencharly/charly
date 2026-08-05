package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/opencharly/spec/spec"
)

// devices.go — the KEPT core GPU/device surface. What remains here is the ONE thing genuinely
// tied to the fenced host_build_pod_config_seams.go / gpu_allocate.go consumers (K5 seam-death,
// in progress — the B6 unit that relocates those two files' GPU legs to peer InvokeProvider
// dispatch is what finally dissolves gpu_shim.go/devices.go entirely; not yet landed):
// LogDetectedDevices, the pure host-independent formatting helper host_build_pod_config_seams.go
// calls after its DetectHostDevices() shim call.
//
// The embedded device_patterns/gpu_vendors/pci_class_labels data tables that used to live here
// moved to candy/plugin-gpu's OWN embed (data.go/data.yml) — plugin-gpu is the actual detection
// consumer, so it is the one data source (R3), not core; core no longer threads them through
// spec.GpuProbeInput (see gpu_shim.go). deviceDescriptions moved to candy/plugin-doctor's own
// embed: that plugin gathers its host facts itself (candy/plugin-doctor/hostfacts.go, peer
// InvokeProvider to verb:gpu/verb:credential), so no core seam threads them any more.
// appendEnvUnique was dead code (zero real callers) — candy/plugin-deploy-pod already carries
// its own independent copy (config_setup_helpers.go).

// AutoDetectFlags provides --no-autodetect CLI flag via Kong.
// Embed in command structs that support device auto-detection.
type AutoDetectFlags struct {
	NoAutoDetect bool `long:"no-autodetect" help:"Disable automatic device detection"`
}

// LogDetectedDevices prints detected devices to stderr.
func LogDetectedDevices(detected spec.DetectedDevices) {
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
