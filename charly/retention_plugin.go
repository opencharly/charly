package main

import (
	"context"
	"fmt"
	"os"

	"github.com/opencharly/sdk/spec"
)

// retention_plugin.go is the CORE adapter for the externalized retention engine
// (candy/plugin-clean, verb:retention) — the K1-alpha core-minimization relocation
// of the former charly/retention.go (the prune engine) + charly/host_build_retention.go
// (the HostBuild seam the plugin used to reach it). Mirrors the
// credential/gpu/tunnel core-adapter pattern (charly/credential_plugin.go,
// charly/gpu_shim.go, charly/tunnel_plugin.go): core resolves+Invokes the
// compiled-in verb:retention word instead of holding the engine itself.
//
// Both core call sites here already run LoadConfig in-process (they ARE core), so
// they resolve defaults.keep_images/keep_check_runs themselves and pass the
// resolved ints in the request — the plugin's own CLI (`charly clean`) and
// candy/plugin-check's post-run prune hook are NOT core and reach the SAME
// resolution via the small "retention-defaults" HostBuild seam
// (host_build_retention_defaults.go) instead.

// pruneAfterBuild runs the post-build retention prune via verb:retention
// (BuildPrune scope: tag retention + stale .build/_candy staging dirs ONLY — the
// narrow historic pruneAfterBuild scope, never the fuller dangling-image/staging
// sweep the `--images` category runs). Best-effort, warn-only, unchanged from
// before the relocation. Runs unconditionally (even keep<=0) because
// pruneBuildCandyDirs always clears the legacy .build/_layers dir regardless of
// the per-candy retention count.
func pruneAfterBuild(dir string) {
	cfg, err := LoadConfig(dir)
	if err != nil {
		return
	}
	keep := resolveIntPtr(cfg.Defaults.KeepImages)
	prov, ok := providerRegistry.resolve(ClassVerb, "retention")
	if !ok {
		fmt.Fprintln(os.Stderr, "Warning: image retention prune: verb:retention not registered (candy/plugin-clean must be compiled in)")
		return
	}
	reply, err := invokeTyped[spec.RetentionRequest, spec.RetentionReply](context.Background(), prov, "retention", OpRun,
		spec.RetentionRequest{Dir: dir, BuildPrune: true, KeepImages: keep})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: image retention prune: %v\n", err)
		return
	}
	if reply.Error != "" {
		fmt.Fprintf(os.Stderr, "Warning: image retention prune: %s\n", reply.Error)
		return
	}
	if len(reply.ImageRefs) > 0 {
		fmt.Fprintf(os.Stderr, "Pruned %d old image tag(s) (keep_images=%d)\n", len(reply.ImageRefs), keep)
	}
	if len(reply.BuildDirs) > 0 {
		fmt.Fprintf(os.Stderr, "Pruned %d build-staging dir(s) under .build/_candy\n", len(reply.BuildDirs))
	}
}

// listCharlyImageTags fetches the read-only tag inventory via verb:retention
// (List: true) — the `charly box list tags` backing call, replacing the former
// direct in-core charlyImageTags call now that the inventory lives in the plugin.
func listCharlyImageTags(dir string) ([]spec.TagInfo, error) {
	prov, ok := providerRegistry.resolve(ClassVerb, "retention")
	if !ok {
		return nil, fmt.Errorf("verb:retention not registered (candy/plugin-clean must be compiled in)")
	}
	reply, err := invokeTyped[spec.RetentionRequest, spec.RetentionReply](context.Background(), prov, "retention", OpRun,
		spec.RetentionRequest{Dir: dir, List: true})
	if err != nil {
		return nil, err
	}
	if reply.Error != "" {
		return nil, fmt.Errorf("%s", reply.Error)
	}
	return reply.TagGroups, nil
}
