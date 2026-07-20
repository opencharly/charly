package main

import (
	"context"

	"github.com/opencharly/sdk/spec"
)

// host_build_pod_remove.go — the "pod-remove" F10 host-builder. The `charly remove` command
// moved to command:remove (candy/plugin-pod, the DEPLOY wave), but its body — quadlet/companion-
// service teardown, pre_remove hooks, purge, deploy-entry cleanup — is deeply core-type-coupled
// (BoxMetadata/ExtractMetadata/sidecar resolution/deploykit.CleanDeployEntry), so it STAYS CORE,
// mirroring host_build_pod_start.go's shape. Core keeps the whole podRemoveCmd orchestration
// (commands.go); the plugin's thin `remove` command forwards its flags via
// HostBuild("pod-remove") and this builder runs the existing remove orchestration VERBATIM.
const podRemoveBuilderKind = "pod-remove"

func hostBuildPodRemove(_ context.Context, req spec.PodRemoveRequest, _ buildEngineContext) (spec.PodRemoveReply, error) {
	cmd := podRemoveCmd{
		Box:        req.Box,
		Instance:   req.Instance,
		Purge:      req.Purge,
		KeepDeploy: req.KeepDeploy,
		Env:        req.Env,
	}
	return spec.PodRemoveReply{}, cmd.Run()
}

var _ = func() bool {
	registerHostBuilder(podRemoveBuilderKind, typedHostBuilder(podRemoveBuilderKind, hostBuildPodRemove))
	return true
}()
