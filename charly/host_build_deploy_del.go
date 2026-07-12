package main

import (
	"context"

	"github.com/opencharly/sdk/spec"
)

// host_build_deploy_del.go — the "deploy-del" F10 host-builder. The `charly bundle del`
// command moved to command:bundle (candy/plugin-bundle, P13); the teardown KERNEL
// (resolveDelNode → ResolveTarget → Del, replaying the recorded ReverseOps) STAYS CORE.
// The plugin's thin `bundle del` command forwards its flags via HostBuild("deploy-del")
// and this builder runs the existing del orchestration VERBATIM in-process. The live
// ReverseRunner is NOT carried on the wire — a programmatic teardown that needs a specific
// runner (the vm guest-SSH reverse runner) is a host-side path, resolved during dispatch;
// so the reconstructed deployDelCmd leaves Runner nil (the local-exec fallback). Generic
// action noun (F11).
const deployDelBuilderKind = "deploy-del"

func hostBuildDeployDel(_ context.Context, req spec.DeployDelRequest, _ buildEngineContext) (spec.DeployDelReply, error) {
	cmd := deployDelCmd{
		Name:            req.Name,
		AssumeYes:       req.AssumeYes,
		KeepRepoChanges: req.KeepRepoChanges,
		KeepServices:    req.KeepServices,
		KeepImage:       req.KeepImage,
		DryRun:          req.DryRun,
	}
	return spec.DeployDelReply{}, cmd.Run()
}

var _ = func() bool {
	registerHostBuilder(deployDelBuilderKind, typedHostBuilder(deployDelBuilderKind, hostBuildDeployDel))
	return true
}()
