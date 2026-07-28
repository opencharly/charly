package main

import (
	"context"
	"fmt"

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
// The core call site here (listCharlyImageTags, `charly box list tags`) already runs
// in-process, so it needs no defaults resolution. The post-build prune (formerly
// core's pruneAfterBuild) moved to candy/plugin-box's dispatchBuild in P8b: the
// plugin resolves defaults.keep_images via the small "retention-defaults" HostBuild
// seam (host_build_retention_defaults.go — the SAME seam `charly clean` and
// candy/plugin-check's post-run prune hook use) and Invokes verb:retention itself.

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
