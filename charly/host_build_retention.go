package main

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/opencharly/sdk/spec"
)

// host_build_retention.go — the generic "retention" F10 host-builder. The externalized `charly clean`
// command plugin (candy/plugin-clean), running ON the host but out-of-process, OWNS the clean command
// (flag grammar, category orchestration, output, the pkg/arch makepkg sweep) and asks the host to run
// the SHARED retention engine via Executor.HostBuild("retention", spec.RetentionRequest{...}). The
// engine (pruneImagesByRetention / pruneBuildCandyDirs / pruneCheckRuns / invalidateImageTags) STAYS
// core because it is multi-caller (charly box build, charly check run, charly box list tags all prune
// too) + needs the core image inventory + OCI-label parsing — so it is reached via this generic action
// noun, NOT a provider word (F11). This resolves the retention counts host-side (ResolveRuntime +
// LoadConfig defaults + the --keep override) and returns the removed refs/dirs/paths + effective counts.
const retentionBuilderKind = "retention"

func hostBuildRetention(_ context.Context, req spec.RetentionRequest, _ buildEngineContext) (spec.RetentionReply, error) {
	rt, err := ResolveRuntime()
	if err != nil {
		return spec.RetentionReply{Error: err.Error()}, nil
	}
	engineBin := EngineBinary(rt.BuildEngine)

	// --invalidate: targeted image-tag invalidation ONLY (matches CleanCmd's early return).
	if req.Invalidate != "" {
		refs, ierr := invalidateImageTags(engineBin, req.Invalidate, req.DryRun)
		if ierr != nil {
			return spec.RetentionReply{Error: fmt.Sprintf("invalidating image tags: %v", ierr)}, nil
		}
		return spec.RetentionReply{ImageRefs: refs}, nil
	}

	// Resolve retention counts: project defaults.keep_* over the fallbacks, then the --keep override.
	keepImages := resolveIntPtr(nil, nil, keepImagesFallback)
	keepCheck := resolveIntPtr(nil, nil, keepCheckRunsFallback)
	if cfg, cerr := LoadConfig(req.Dir); cerr == nil {
		keepImages = resolveIntPtr(cfg.Defaults.KeepImages, nil, keepImagesFallback)
		keepCheck = resolveIntPtr(cfg.Defaults.KeepCheckRuns, nil, keepCheckRunsFallback)
	}
	if req.Keep > 0 {
		keepImages, keepCheck = req.Keep, req.Keep
	}
	reply := spec.RetentionReply{KeepImages: keepImages, KeepCheckRuns: keepCheck}

	if req.Images {
		refs, perr := pruneImagesByRetention(engineBin, keepImages, req.DryRun)
		if perr != nil {
			return spec.RetentionReply{Error: fmt.Sprintf("pruning images: %v", perr)}, nil
		}
		reply.ImageRefs = refs
		dangling, derr := pruneDanglingCharlyImages(engineBin, req.DryRun)
		if derr != nil {
			return spec.RetentionReply{Error: fmt.Sprintf("pruning dangling images: %v", derr)}, nil
		}
		reply.DanglingIDs = dangling
		reply.StagingDirs = pruneBuildahStaging(req.DryRun)
		reply.BuildDirs = pruneBuildCandyDirs(filepath.Join(req.Dir, ".build"), keepImages, req.DryRun)
	}
	if req.Check {
		paths, perr := pruneCheckRuns(filepath.Join(req.Dir, ".check"), keepCheck, req.DryRun)
		if perr != nil {
			return spec.RetentionReply{Error: fmt.Sprintf("pruning check runs: %v", perr)}, nil
		}
		reply.CheckPaths = paths
	}
	return reply, nil
}

var _ = func() bool {
	registerHostBuilder(retentionBuilderKind, typedHostBuilder(retentionBuilderKind, hostBuildRetention))
	return true
}()
