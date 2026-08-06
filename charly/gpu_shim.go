package main

import (
	"context"

	"github.com/opencharly/spec/ops"
	"github.com/opencharly/spec/spec"
)

// gpu_shim.go — the in-core SHIM for the operator-dropped GPU-host-DETECTION exception (same
// family as gpu_allocate.go's EXCEPTION-GPU row: no hardware to verify a relocation against).
// The sysfs/exec detection LOGIC lives in the COMPILED-IN candy/plugin-gpu (verb:gpu); DetectVFIO
// resolves that provider and Invokes it. The detect-host-devices/ensure-cdi shims +
// LogDetectedDevices relocated to candy/plugin-deploy-pod as peer verb:gpu dispatches (K-wave 2
// cone R3).

// gpuProbeReply resolves verb:gpu and Invokes it with the action-multiplexed input.
func gpuProbeReply(in spec.GpuProbeInput) spec.GpuProbeReply {
	return hostInvokeOr[spec.GpuProbeInput, spec.GpuProbeReply](context.Background(), ClassVerb, "gpu", ops.OpRun, in, "gpu probe "+in.Action)
}

// DetectVFIO probes the host for IOMMU readiness + passthrough-capable GPUs (package var for
// testability). gpu_allocate.go's bedGPUPrereqMissing is the one remaining core caller.
var DetectVFIO = func() spec.VFIOReport {
	reply := gpuProbeReply(spec.GpuProbeInput{Action: "detect-vfio"})
	if reply.Vfio == nil {
		return spec.VFIOReport{}
	}
	return *reply.Vfio
}
