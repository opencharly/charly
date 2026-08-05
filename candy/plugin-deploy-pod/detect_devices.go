package deploypod

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/opencharly/sdk"
	"github.com/opencharly/spec/spec"
)

// detect_devices.go — the GPU/device host-DETECTION leg RELOCATED from the deleted
// "pod-config-detect-devices" HostBuild seam + charly/gpu_shim.go's DetectHostDevices/EnsureCDI
// shims + charly/devices.go's LogDetectedDevices (K-wave 2 cone R3): the plugin probes the host
// peer-to-peer via InvokeProvider(verb:gpu) — the SAME dispatch the core shims made through
// hostInvokeOr (best-effort, never-fail: a probe miss degrades to a zero reply + a loud stderr
// note, matching the original detection semantics). The core gpu_shim.go keeps only the
// DetectVFIO exception leg (gpu_allocate.go's bedGPUPrereqMissing).

// gpuProbe dispatches one verb:gpu detection action and returns the reply. Never fails: an
// InvokeProvider error degrades to a zero reply + a loud stderr note (the core hostInvokeOr
// semantics).
func gpuProbe(ctx context.Context, ex *sdk.Executor, action string) spec.GpuProbeReply {
	var reply spec.GpuProbeReply
	if ex == nil {
		fmt.Fprintf(os.Stderr, "warning: gpu probe %s: no host reverse channel\n", action)
		return reply
	}
	inJSON, err := json.Marshal(spec.GpuProbeInput{Action: action})
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: gpu probe %s: %v\n", action, err)
		return reply
	}
	resJSON, err := ex.InvokeProvider(ctx, "verb", "gpu", sdk.OpRun, inJSON, nil, sdk.InvokeProviderOpts{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: gpu probe %s: %v\n", action, err)
		return reply
	}
	if len(resJSON) > 0 {
		if uerr := json.Unmarshal(resJSON, &reply); uerr != nil {
			fmt.Fprintf(os.Stderr, "warning: gpu probe %s: %v\n", action, uerr)
			return spec.GpuProbeReply{}
		}
	}
	return reply
}

// detectDevices probes the host for GPU/device nodes, logs them, and ensures the NVIDIA CDI spec
// when a GPU is present and the engine is podman — the relocated body of the deleted
// hostBuildPodConfigDetectDevices seam. noAutoDetect skips detection (and therefore the CDI
// ensure, which only fires on a detected GPU); engine "podman" alongside a detection triggers the
// ensure-cdi probe (the pod lifecycle's resolvePodRuntimeImage step bundles it into the SAME
// call, R3 — the former seam's exact sequencing).
func detectDevices(ctx context.Context, ex *sdk.Executor, noAutoDetect bool, engine string) spec.DetectedDevices {
	var detected spec.DetectedDevices
	if !noAutoDetect {
		reply := gpuProbe(ctx, ex, "detect-host-devices")
		if reply.HostDevices != nil {
			detected = *reply.HostDevices
		}
		logDetectedDevices(detected)
	}
	if detected.GPU && engine == "podman" {
		gpuProbe(ctx, ex, "ensure-cdi")
	}
	return detected
}

// logDetectedDevices prints detected devices to stderr — relocated verbatim from
// charly/devices.go's LogDetectedDevices (its only core caller, the detect-devices seam, died
// with this cone).
func logDetectedDevices(detected spec.DetectedDevices) {
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
