package main

import (
	"context"

	"github.com/opencharly/spec/spec"
)

// host_build_deploy_from_box.go — the "deploy-from-box" F10 host-builder. The `charly
// bundle from-box` command moved to command:bundle (candy/plugin-bundle, P13); the pod-path
// source-less deploy dispatch (deployFromBoxCmd.Run, which forwards to the deploy:pod plugin's
// config-setup ORCHESTRATION via hostBuildPodConfigSetup, P13-KERNEL direction-flip) STAYS CORE —
// the k8s path (--cluster) no longer reaches this seam at all (Cone A shape 3): the plugin's
// BundleFromBoxCmd.Run() branches BEFORE forwarding and handles --cluster ENTIRELY plugin-side
// (candy/plugin-bundle/deploy_from_box.go). The plugin's thin `bundle from-box` command forwards
// its POD-path flags via HostBuild("deploy-from-box") and this builder runs the existing
// orchestration VERBATIM in-process. Generic action noun (F11).
const deployFromBoxBuilderKind = "deploy-from-box"

func hostBuildDeployFromBox(_ context.Context, req spec.DeployFromBoxRequest, _ buildEngineContext) (spec.DeployFromBoxReply, error) {
	cmd := deployFromBoxCmd{
		Ref:      req.Ref,
		Name:     req.Name,
		Instance: req.Instance,
		Env:      req.Env,
		Port:     req.Port,
	}
	return spec.DeployFromBoxReply{}, cmd.Run()
}

var _ = func() bool {
	registerHostBuilder(deployFromBoxBuilderKind, typedHostBuilder(deployFromBoxBuilderKind, hostBuildDeployFromBox))
	return true
}()
