package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/opencharly/sdk/spec"
)

// --- GPU driver-switch consts + pure helpers, aliased from spec (cutover C9) ---
//
// The DRIVER-SWITCH logic moved into candy/plugin-gpu; a handful of core sites (vm_gpu_cmd
// shims, the arbiter mode-math seam, gpu_allocate) still name these values/helpers, so core
// aliases the ONE spec copy (R3 — no duplicate, no drift).

const (
	gpuModeVfio       = spec.GpuModeVfio
	gpuModeNvidia     = spec.GpuModeNvidia
	nvidiaVendorID    = spec.NvidiaVendorID
	hostDriverDisplay = spec.HostDriverDisplay
	hostDriverAudio   = spec.HostDriverAudio
	hostDriverVfio    = spec.HostDriverVfio
)

// errGPUSwitchWedged is the driver-switch wedge sentinel (spec.ErrGPUSwitchWedged). The
// gpu shims re-wrap it from the plugin's GpuSwitchReply.Wedged bool so callers (vm_gpu_cmd)
// keep matching with errors.Is.
var errGPUSwitchWedged = spec.ErrGPUSwitchWedged

// normalizePCIVendor / selectGPUByVendor are the pure GPU-selection helpers (spec) used by
// auto-allocation (gpu_allocate.go) + vm_gpu_cmd; kept as package-var aliases so those call
// sites are unchanged.
var (
	normalizePCIVendor = spec.NormalizePCIVendor
	selectGPUByVendor  = spec.SelectGPUByVendor
)

// gpu_shim.go — the in-core SHIMS for GPU/VFIO host detection (cutover C11). The
// sysfs/exec detection LOGIC moved into the COMPILED-IN candy/plugin-gpu (verb:gpu);
// these shims resolve that provider and Invoke it, so the ~10 in-core consumers
// (config_image.go/start.go/shell.go CDI-env sites, `charly doctor`, `charly vm gpu`,
// `charly vm create`, and gpu_allocate.go which already calls DetectVFIO) compile
// against the SAME symbol names and are invisible above the shim. (The C9 driver-switch
// shims below dispatch verb:gpu's DRIVER-SWITCH actions the same way.)
//
// host→plugin dispatch mirrors k8sgen/egress (plain resolve+Invoke). Compiled-in
// placement keeps verb:gpu resolvable with no connect step and runs the probe IN-PROC
// (so MemlockLimitBytes reads charly's OWN RLIMIT_MEMLOCK — the semantics the callers
// expect). The detection RESULT types alias package spec so consumers keep referring
// to VFIOReport/VFIOGpu/VFIOPCIDevice/DetectedDevices unchanged.

// Type aliases — the detection result types live in package spec (the SDK-importable
// home the plugin also constructs them from) and are aliased here so every package-main
// consumer compiles unchanged (R3, invisible above the shim).
type (
	VFIOReport      = spec.VFIOReport
	VFIOGpu         = spec.VFIOGpu
	VFIOPCIDevice   = spec.VFIOPCIDevice
	DetectedDevices = spec.DetectedDevices
)

// gpuProbeReply resolves verb:gpu and Invokes it with the action-multiplexed input.
// plugin-gpu is compiled-in, so resolve never misses in a correctly-built binary; a
// miss (charly built without candy/plugin-gpu) degrades to a zero reply + a loud
// stderr note rather than crashing a hot deploy path — matching the original
// best-effort, never-fail detection semantics.
func gpuProbeReply(in spec.GpuProbeInput) spec.GpuProbeReply {
	prov, ok := providerRegistry.resolve(ClassVerb, "gpu")
	if !ok {
		fmt.Fprintln(os.Stderr, "warning: gpu plugin (verb:gpu) not registered — charly built without candy/plugin-gpu; GPU/VFIO detection unavailable")
		return spec.GpuProbeReply{}
	}
	params, err := marshalJSON(in)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: gpu probe marshal (%s): %v\n", in.Action, err)
		return spec.GpuProbeReply{}
	}
	res, err := prov.Invoke(context.Background(), &Operation{Reserved: "gpu", Op: OpRun, Params: params})
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: gpu probe %s: %v\n", in.Action, err)
		return spec.GpuProbeReply{}
	}
	var reply spec.GpuProbeReply
	if res != nil && len(res.JSON) > 0 {
		if err := json.Unmarshal(res.JSON, &reply); err != nil {
			fmt.Fprintf(os.Stderr, "warning: gpu probe %s decode: %v\n", in.Action, err)
			return spec.GpuProbeReply{}
		}
	}
	return reply
}

// DetectGPU checks whether an NVIDIA GPU is usable via CDI (driver loaded AND a CDI
// spec reachable or nvidia-ctk on PATH). Package-level var for testability (tests swap
// it with a fake); the real probe runs in candy/plugin-gpu.
var DetectGPU = func() bool {
	return gpuProbeReply(spec.GpuProbeInput{Action: "detect-gpu"}).Bool
}

// DetectAMDGPU checks whether an AMD GPU is available (amdgpu DRM driver bound).
// Package-level var for testability.
var DetectAMDGPU = func() bool {
	return gpuProbeReply(spec.GpuProbeInput{Action: "detect-amd-gpu"}).Bool
}

// DetectVFIO probes the host for IOMMU readiness and passthrough-capable GPUs.
// Package-level var for testability (mirrors DetectGPU). The pci_class_labels table
// stays in core (devices.go); the shim threads it to the plugin.
var DetectVFIO = func() VFIOReport {
	reply := gpuProbeReply(spec.GpuProbeInput{Action: "detect-vfio", PCIClassLabels: pciClassLabels})
	if reply.Vfio == nil {
		return VFIOReport{}
	}
	return *reply.Vfio
}

// DetectHostDevices probes the host for available devices. Package-level var for
// testability. The device_patterns + gpu_vendors tables stay in core (devices.go); the
// shim threads them to the plugin.
var DetectHostDevices = func() DetectedDevices {
	reply := gpuProbeReply(spec.GpuProbeInput{
		Action:         "detect-host-devices",
		DevicePatterns: devicePatterns,
		GpuVendors:     gpuRenderVendors,
	})
	if reply.HostDevices == nil {
		return DetectedDevices{}
	}
	return *reply.HostDevices
}

// EnsureCDI generates the NVIDIA CDI spec via nvidia-ctk if none exists (user-scope,
// best-effort). The generation runs in candy/plugin-gpu.
func EnsureCDI() { gpuProbeReply(spec.GpuProbeInput{Action: "ensure-cdi"}) }

// MemlockLimitBytes returns the current process's RLIMIT_MEMLOCK (soft, hard). VFIO
// passthrough pins all guest RAM, so QEMU needs a memlock limit ≥ guest RAM. Runs
// IN-PROC in the compiled-in plugin, so it reads charly's own limit.
func MemlockLimitBytes() (soft, hard uint64) { //nolint:unparam // soft returned for rlimit-pair API completeness
	reply := gpuProbeReply(spec.GpuProbeInput{Action: "memlock"})
	return reply.MemlockSoft, reply.MemlockHard
}

// VfioGroupAccessible reports whether the current user can open the VFIO group device
// node (/dev/vfio/<group>). group < 0 → no IOMMU group.
func VfioGroupAccessible(group int) bool {
	if group < 0 {
		return false
	}
	return gpuProbeReply(spec.GpuProbeInput{Action: "vfio-group-accessible", Group: group}).Bool
}

// detectAMDGFXVersion reads the AMD GPU architecture version from KFD topology (e.g.
// "10.3.0"), for `charly doctor`'s HSA_OVERRIDE_GFX_VERSION hint. The read runs in
// candy/plugin-gpu.
func detectAMDGFXVersion() string {
	return gpuProbeReply(spec.GpuProbeInput{Action: "amd-gfx-version"}).Str
}

// --- GPU DRIVER-SWITCH shims (cutover C9) -----------------------------------------
//
// The vfio<->nvidia rebind primitive moved into candy/plugin-gpu (1B). These shims resolve
// verb:gpu and Invoke the DRIVER-SWITCH actions (spec.GpuSwitchInput/GpuSwitchReply), so the
// consumers — `charly vm gpu` (vm_gpu_cmd.go) + the arbiter's switchMode/ensureCDI host-seams
// (arbiter_host.go) — call the SAME symbol names and are invisible above the shim (R3). Same
// resolve+Invoke pattern as the detection shims above.

// gpuSwitchReply resolves verb:gpu and Invokes it with a driver-switch action. A miss (charly
// built without candy/plugin-gpu) degrades to a zero reply + a loud stderr note, matching the
// detection shims' never-crash contract.
func gpuSwitchReply(in spec.GpuSwitchInput) spec.GpuSwitchReply {
	prov, ok := providerRegistry.resolve(ClassVerb, "gpu")
	if !ok {
		fmt.Fprintln(os.Stderr, "warning: gpu plugin (verb:gpu) not registered — charly built without candy/plugin-gpu; GPU driver-switch unavailable")
		return spec.GpuSwitchReply{}
	}
	params, err := marshalJSON(in)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: gpu switch marshal (%s): %v\n", in.Action, err)
		return spec.GpuSwitchReply{}
	}
	res, err := prov.Invoke(context.Background(), &Operation{Reserved: "gpu", Op: OpRun, Params: params})
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: gpu switch %s: %v\n", in.Action, err)
		return spec.GpuSwitchReply{Error: err.Error()}
	}
	var reply spec.GpuSwitchReply
	if res != nil && len(res.JSON) > 0 {
		if err := json.Unmarshal(res.JSON, &reply); err != nil {
			fmt.Fprintf(os.Stderr, "warning: gpu switch %s decode: %v\n", in.Action, err)
			return spec.GpuSwitchReply{}
		}
	}
	return reply
}

// switchReplyErr maps a GpuSwitchReply's op result to an error: a wedge re-wraps
// errGPUSwitchWedged (so errors.Is matches across the process boundary); a non-wedge failure
// is the plain reply error; success is nil.
func switchReplyErr(r spec.GpuSwitchReply) error {
	if r.Wedged {
		if d := strings.TrimSpace(r.Error); d != "" {
			return fmt.Errorf("%w\n%s", errGPUSwitchWedged, d)
		}
		return errGPUSwitchWedged
	}
	if r.Error != "" {
		return errors.New(r.Error)
	}
	return nil
}

// switchGPUDriverMode flips a SPECIFIC card's whole IOMMU group to mode (the `charly vm gpu
// mode`/`recover` exact-card path). Errors with errGPUSwitchWedged on a device_lock wedge.
func switchGPUDriverMode(gpu VFIOGpu, mode string) error {
	return switchReplyErr(gpuSwitchReply(spec.GpuSwitchInput{Action: spec.GpuSwitchActionMode, Gpu: &gpu, Mode: mode}))
}

// gpuSwitchModeTolerant flips the vendor-matched card's group to mode, TOLERANT of an absent
// card (the arbiter's switchMode seam — a claim stays portable across GPU/no-GPU hosts).
// Returns wedged so the arbiter can carry the wedge over its own reverse channel + poison.
func gpuSwitchModeTolerant(vendor, mode string) (wedged bool, err error) {
	r := gpuSwitchReply(spec.GpuSwitchInput{Action: spec.GpuSwitchActionMode, Vendor: vendor, Mode: mode})
	return r.Wedged, switchReplyErr(r)
}

// ensureCDIRoot (re)generates /etc/cdi/nvidia.yaml as root after a flip to nvidia.
func ensureCDIRoot() { gpuSwitchReply(spec.GpuSwitchInput{Action: spec.GpuSwitchActionEnsureCDI}) }

// gpuWedgeDetected is the read-only device_lock-wedge probe (`charly vm gpu status`/`recover`).
func gpuWedgeDetected() bool {
	return gpuSwitchReply(spec.GpuSwitchInput{Action: spec.GpuSwitchActionWedgeDetected}).Bool
}

// groupInMode reports whether a GPU's whole IOMMU group is already in mode (the idempotency
// gate; `charly vm gpu`).
func groupInMode(gpu VFIOGpu, mode string) bool {
	return gpuSwitchReply(spec.GpuSwitchInput{Action: spec.GpuSwitchActionGroupInMode, Gpu: &gpu, Mode: mode}).Bool
}

// currentGPUMode reports the live mode of a GPU's display function (`charly vm gpu`).
func currentGPUMode(gpu VFIOGpu) string {
	return gpuSwitchReply(spec.GpuSwitchInput{Action: spec.GpuSwitchActionCurrentMode, Gpu: &gpu}).Str
}

// gpuDisplayDriver reads the live driver of a single PCI function (`charly vm gpu` status).
func gpuDisplayDriver(addr string) string {
	return gpuSwitchReply(spec.GpuSwitchInput{Action: spec.GpuSwitchActionDisplayDriver, Addr: addr}).Str
}

// gpuSwitchPlan returns the EXACT vfio/nvidia rebind commands for mode WITHOUT touching sysfs —
// the cred/hardware-free DRY-RUN dispatch proof (`charly vm gpu plan`). gpu==nil makes the plugin
// synthesize a documented example card, so the plan is available on a GPU-less host.
func gpuSwitchPlan(gpu *VFIOGpu, mode string) ([]string, error) {
	in := spec.GpuSwitchInput{Action: spec.GpuSwitchActionPlan, Mode: mode}
	if gpu != nil {
		in.Gpu = gpu
	}
	r := gpuSwitchReply(in)
	return r.Plan, switchReplyErr(r)
}
