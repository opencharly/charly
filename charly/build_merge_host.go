package main

import (
	"context"

	"github.com/opencharly/sdk/spec"
)

// build_merge_host.go — the TRANSITIONAL "merge" host-builder behind the
// candy/plugin-build DRIVE (P8b). The candy's build loop gates on a box's
// MergeAuto and, when set, asks the host to run the post-build inline layer merge
// via HostBuild("merge"). Merge lives in charly/merge.go (go-containerregistry) —
// owned by the P14a OCI cutover (registry.go+merge.go → verb:oci), which lands
// AFTER P8b (C7). This seam is DELIBERATELY TRANSITIONAL: when P14a lands, the
// candy swaps its HostBuild("merge", …) call to InvokeProvider("verb","oci",…) and
// THIS file (registerHostBuilder("merge", …)) DELETES. The request/reply wire
// types (spec.MergeRequest/MergeReply) are the SHARED contract P14a consumes (R3;
// they may relocate into the oci schema in the P14a cutover).
//
// "merge" is a CLASS-GENERIC action noun (the F11 uniform-API gate), never a
// provider word.

// hostBuildMerge runs the inline layer merge for one just-built image REF via the
// shared ref-based engine (mergeImageRef — the same body MergeCmd.runOne calls,
// R3), mirroring the former in-drive mergeAfterBuild. The candy passes the resolved
// ImageRef + Engine + the per-box MaxMB/MaxTotalMB (box config, 0 → project
// defaults here — matching runOne's box-config-or-default lookup, since the pure
// ref merge cannot re-resolve the box). A merge FAILURE rides MergeReply.Error (the
// reply-error convention) so the candy warns without failing the build, exactly as
// the host-side mergeAfterBuild warned; the image stays functional-but-unmerged.
func hostBuildMerge(_ context.Context, req spec.MergeRequest, _ buildEngineContext) (spec.MergeReply, error) {
	maxMB := req.MaxMB
	if maxMB <= 0 {
		maxMB = defaultMaxMB
	}
	maxTotalMB := req.MaxTotalMB
	if maxTotalMB <= 0 {
		maxTotalMB = defaultMaxTotalMB
	}
	engine := req.Engine
	if engine == "" {
		rt, err := ResolveRuntime()
		if err != nil {
			return spec.MergeReply{Error: errString(err)}, nil
		}
		engine = rt.BuildEngine
	}
	maxBytes := int64(maxMB) * 1024 * 1024
	if err := mergeImageRef(req.ImageRef, engine, maxBytes, maxTotalMB, req.DryRun); err != nil {
		return spec.MergeReply{Error: errString(err)}, nil
	}
	return spec.MergeReply{}, nil
}

var _ = func() bool {
	registerHostBuilder("merge", typedHostBuilder("merge", hostBuildMerge))
	return true
}()
