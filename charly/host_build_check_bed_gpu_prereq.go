package main

import (
	"context"

	"github.com/opencharly/spec/spec"
)

// host_build_check_bed_gpu_prereq.go — the ONE narrow seam surviving check-bed's full
// dissolution (#55 W3 B2-full): GPU host-DETECTION (gpu_allocate.go's bedGPUPrereqMissing,
// DetectVFIO) is the project's explicitly operator-dropped exception — no hardware on this host
// to verify a relocation against, so gpu_shim.go's own header fences it from every K-wave cutover,
// this one included ("Per the W3 adjudication (unit B6)... landing B6 is what finally deletes this
// file"). This seam threads just the claimant's pre-computed resource tokens out and the
// GPU-unsatisfiable verdict back, so the fenced core logic (bedGPUPrereqMissing/gpuPrereqMissing/
// DetectVFIO) is reached UNCHANGED — candy/plugin-check/bed_session.go calls this instead of
// self-orchestrating GPU detection itself.
const checkBedGpuPrereqBuilderKind = "check-bed-gpu-prereq"

// hostBuildCheckBedGpuPrereq reconstructs a minimal spec.BundleNode carrying only the
// already-deduped token set bedGPUPrereqMissing reads (via its RequiredExclusive/RequiredShared
// accessors) — the fenced function itself is untouched.
func hostBuildCheckBedGpuPrereq(_ context.Context, req spec.CheckBedGpuPrereqRequest, _ buildEngineContext) (spec.CheckBedGpuPrereqReply, error) {
	node := spec.BundleNode{RequiresExclusive: req.Tokens}
	token, vendor, missing := bedGPUPrereqMissing(node)
	return spec.CheckBedGpuPrereqReply{Missing: missing, Token: token, Vendor: vendor}, nil
}

var _ = func() bool {
	registerHostBuilder(checkBedGpuPrereqBuilderKind, typedHostBuilder(checkBedGpuPrereqBuilderKind, hostBuildCheckBedGpuPrereq))
	return true
}()
