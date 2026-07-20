package main

import (
	"context"

	"github.com/opencharly/sdk/spec"
)

// host_build_deploy_from_box.go — the "deploy-from-box" F10 host-builder. The `charly
// bundle from-box` command moved to command:bundle (candy/plugin-bundle, P13); the
// source-less deploy dispatch (deployFromBoxCmd.Run, which forwards the pod path to the
// deploy:pod plugin's config-setup ORCHESTRATION via hostBuildPodConfigSetup — P13-KERNEL
// direction-flip — or the K8s Kustomize path with --cluster) STAYS CORE. The plugin's thin
// `bundle from-box` command forwards its flags via HostBuild("deploy-from-box") and this
// builder runs the existing from-box orchestration VERBATIM in-process. Generic action noun (F11).
const deployFromBoxBuilderKind = "deploy-from-box"

func hostBuildDeployFromBox(_ context.Context, req spec.DeployFromBoxRequest, _ buildEngineContext) (spec.DeployFromBoxReply, error) {
	cmd := deployFromBoxCmd{
		Ref:       req.Ref,
		Name:      req.Name,
		Instance:  req.Instance,
		Env:       req.Env,
		Port:      req.Port,
		Cluster:   req.Cluster,
		Namespace: req.Namespace,
	}
	return spec.DeployFromBoxReply{}, cmd.Run()
}

var _ = func() bool {
	registerHostBuilder(deployFromBoxBuilderKind, typedHostBuilder(deployFromBoxBuilderKind, hostBuildDeployFromBox))
	return true
}()
