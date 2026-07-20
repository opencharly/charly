package main

import (
	"context"

	"github.com/opencharly/sdk/spec"
)

// host_build_pod_update.go — the "pod-update" F10 host-builder. The `charly update` command
// moved to command:update (candy/plugin-pod, the DEPLOY wave), but the LifecycleTarget
// dispatch it drives — resolveTreeRoot/loadDeployPlugins/ResolveTarget, the plugin loader and
// provider registry — STAYS CORE, mirroring host_build_pod_start.go exactly. Core keeps the
// whole podUpdateCmd orchestration (commands.go/update_deploy_dispatch.go); the plugin's thin
// `update` command forwards its flags via HostBuild("pod-update") and this builder runs the
// existing update orchestration VERBATIM. Generic action noun (F11 — never a substrate word).
const podUpdateBuilderKind = "pod-update"

func hostBuildPodUpdate(_ context.Context, req spec.PodUpdateRequest, _ buildEngineContext) (spec.PodUpdateReply, error) {
	cmd := podUpdateCmd{
		Box:       req.Box,
		Tag:       req.Tag,
		Build:     req.Build,
		Instance:  req.Instance,
		Seed:      req.Seed,
		ForceSeed: req.ForceSeed,
		DataFrom:  req.DataFrom,
	}
	return spec.PodUpdateReply{}, cmd.Run()
}

var _ = func() bool {
	registerHostBuilder(podUpdateBuilderKind, typedHostBuilder(podUpdateBuilderKind, hostBuildPodUpdate))
	return true
}()
