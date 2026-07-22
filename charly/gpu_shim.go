package main

import (
	"github.com/opencharly/sdk/spec"
)

// --- GPU pure helpers, aliased from spec ------------------------------------------------------
//
// nvidiaVendorID is still consumed in-core (gpu_imply.go's GPU-implied-shared check);
// normalizePCIVendor/selectGPUByVendor by gpu_allocate.go's auto-allocation. The driver-MODE
// consts (vfio/nvidia) and host-driver-name consts are consumed ONLY by the DRIVER-SWITCH path,
// which is entirely out-of-core now (candy/plugin-gpu + candy/plugin-vm + candy/plugin-preempt
// dispatch verb:gpu peer-to-peer) — no in-core alias needed for those.

const nvidiaVendorID = spec.NvidiaVendorID

// normalizePCIVendor / selectGPUByVendor are the pure GPU-selection helpers (spec) used by
// auto-allocation (gpu_allocate.go); kept as package-var aliases so those call sites are
// unchanged.
var (
	normalizePCIVendor = spec.NormalizePCIVendor
	selectGPUByVendor  = spec.SelectGPUByVendor
)

// gpu_shim.go — the in-core SHIMS for GPU/VFIO host DETECTION (cutover C11). The
// sysfs/exec detection LOGIC moved into the COMPILED-IN candy/plugin-gpu (verb:gpu);
// these shims resolve that provider and Invoke it, so the in-core consumers
// (config_image.go CDI-env sites, `charly doctor`, `charly vm create`, and
// gpu_allocate.go which already calls DetectVFIO) compile against the SAME symbol names
// and are invisible above the shim. The DRIVER-SWITCH path (vfio<->nvidia rebind) has NO
// in-core shim — every consumer (`charly vm gpu`, the arbiter) dispatches verb:gpu directly.
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
	return hostInvokeOr[spec.GpuProbeInput, spec.GpuProbeReply](ClassVerb, "gpu", OpRun, in, "gpu probe "+in.Action)
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

// --- GPU DRIVER-SWITCH ---------------------------------------------------------------------
//
// The vfio<->nvidia rebind primitive lives in candy/plugin-gpu (1B). Every DRIVER-SWITCH
// consumer now dispatches verb:gpu directly rather than through an in-core shim: `charly vm
// gpu` (candy/plugin-vm's vm_gpu_shim.go) and the arbiter's switchMode/ensureCDI (FLOOR-SLIM-
// proper Unit-8 moved these into candy/plugin-preempt/holder_dispatch.go, using the
// class-agnostic sdk.Executor.InvokeProvider) both call verb:gpu peer-to-peer. No in-core
// driver-switch shim remains — only the pure detection shims above (gpuProbeReply and its
// consumers), which stay because `charly doctor`/`config_image.go`/`gpu_allocate.go` are
// genuinely in-core callers.
