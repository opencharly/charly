package main

import (
	"context"
	"github.com/opencharly/spec/ops"
	"github.com/opencharly/spec/spec"
)

// gpu_shim.go — the in-core SHIMS for GPU/VFIO host DETECTION (cutover C11). The
// sysfs/exec detection LOGIC moved into the COMPILED-IN candy/plugin-gpu (verb:gpu);
// these shims resolve that provider and Invoke it. The DRIVER-SWITCH path (vfio<->nvidia
// rebind) has NO in-core shim — every consumer (`charly vm gpu`, the arbiter) dispatches
// verb:gpu directly.
//
// K5 seam-death (in progress): the "hostprobe" HostBuild kind (formerly charly/
// host_build_hostprobe.go, the `charly doctor` consumer) is GONE — candy/plugin-doctor now
// reaches verb:gpu peer-to-peer over InvokeProvider itself, the SAME pattern the arbiter/
// `charly vm gpu` already use. What's LEFT here is genuinely core-coupled: DetectVFIO
// (gpu_allocate.go's bedGPUPrereqMissing) and DetectHostDevices/EnsureCDI
// (host_build_pod_config_seams.go's detect-devices/ensure-image legs) — both fenced,
// active-wave files this cutover cannot touch. Per the W3 adjudication (unit B6), those two
// legs are ALREADY slated to die the same way ("plugin-deploy-pod InvokeProviders verb:gpu
// directly ... seam leg dies") — landing B6 is what finally deletes this file. The three
// static data tables (device_patterns/gpu_vendors/pci_class_labels) no longer thread
// through spec.GpuProbeInput — they are candy/plugin-gpu's OWN embed now (data.go), so this
// file no longer needs a core-side copy of them.
//
// host→plugin dispatch mirrors k8sgen/egress (plain resolve+Invoke). Compiled-in
// placement keeps verb:gpu resolvable with no connect step. W0 deleted the former in-core
// type/const/var ALIASES onto spec.* (the nvidiaVendorID/normalizePCIVendor/
// selectGPUByVendor/VFIOReport/VFIOGpu/VFIOPCIDevice/DetectedDevices re-exports, which
// evaded the *_aliases.go glob by filename) — every consumer reads
// spec.NvidiaVendorID/spec.NormalizePCIVendor/spec.SelectGPUByVendor/spec.VFIOReport/
// spec.VFIOGpu/spec.VFIOPCIDevice/spec.DetectedDevices directly.

// gpuProbeReply resolves verb:gpu and Invokes it with the action-multiplexed input.
// plugin-gpu is compiled-in, so resolve never misses in a correctly-built binary; a
// miss (charly built without candy/plugin-gpu) degrades to a zero reply + a loud
// stderr note rather than crashing a hot deploy path — matching the original
// best-effort, never-fail detection semantics.
func gpuProbeReply(in spec.GpuProbeInput) spec.GpuProbeReply {
	return hostInvokeOr[spec.GpuProbeInput, spec.GpuProbeReply](context.Background(), ClassVerb, "gpu", ops.OpRun, in, "gpu probe "+in.Action)
}

// DetectVFIO probes the host for IOMMU readiness and passthrough-capable GPUs.
// Package-level var for testability. gpu_allocate.go's bedGPUPrereqMissing is the one
// remaining core caller (fenced — see the file header IOU).
var DetectVFIO = func() spec.VFIOReport {
	reply := gpuProbeReply(spec.GpuProbeInput{Action: "detect-vfio"})
	if reply.Vfio == nil {
		return spec.VFIOReport{}
	}
	return *reply.Vfio
}

// DetectHostDevices probes the host for available devices. Package-level var for
// testability. host_build_pod_config_seams.go is the one remaining core caller (fenced —
// see the file header IOU).
var DetectHostDevices = func() spec.DetectedDevices {
	reply := gpuProbeReply(spec.GpuProbeInput{Action: "detect-host-devices"})
	if reply.HostDevices == nil {
		return spec.DetectedDevices{}
	}
	return *reply.HostDevices
}

// EnsureCDI generates the NVIDIA CDI spec via nvidia-ctk if none exists (user-scope,
// best-effort). The generation runs in candy/plugin-gpu. host_build_pod_config_seams.go
// is the one remaining core caller (fenced — see the file header IOU).
func EnsureCDI() { gpuProbeReply(spec.GpuProbeInput{Action: "ensure-cdi"}) }

// --- GPU DRIVER-SWITCH ---------------------------------------------------------------------
//
// The vfio<->nvidia rebind primitive lives in candy/plugin-gpu (1B). Every DRIVER-SWITCH
// consumer now dispatches verb:gpu directly rather than through an in-core shim: `charly vm
// gpu` (candy/plugin-vm's vm_gpu_shim.go) and the arbiter's switchMode/ensureCDI (FLOOR-SLIM-
// proper Unit-8 moved these into candy/plugin-preempt/holder_dispatch.go, using the
// class-agnostic sdk.Executor.InvokeProvider) both call verb:gpu peer-to-peer. No in-core
// driver-switch shim remains — only the pure detection shims above (gpuProbeReply and its
// consumers), which stay because `charly doctor`/`gpu_allocate.go` are
// genuinely in-core callers.
