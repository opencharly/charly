package main

import (
	"context"
	"fmt"

	"github.com/opencharly/spec/spec"
)

// host_build_deploy_node_del_dispatch.go — the "deploy-node-del-dispatch" F10 host-builder
// (K4-C walk port): ResolveTarget + target.Del, honoring the teardown gates. The plugin resolves
// the node (del_resolve.go) + strips the "vm:" addressing prefix before sending; a live
// ReverseRunner is never carried on the wire.
const deployNodeDelDispatchBuilderKind = "deploy-node-del-dispatch"

func hostBuildDeployNodeDelDispatch(_ context.Context, req spec.DeployNodeDelDispatchRequest, _ buildEngineContext) (spec.DeployNodeDelDispatchReply, error) {
	utgt, err := ResolveTarget(req.Node, req.Name)
	if err != nil {
		return spec.DeployNodeDelDispatchReply{}, fmt.Errorf("deploy-node-del-dispatch: resolve target: %w", err)
	}
	if tt, ok := utgt.(*pluginDeployTarget); ok {
		tt.KeepRepoChanges = req.KeepRepoChanges
		tt.KeepServices = req.KeepServices
		tt.KeepImage = req.KeepImage
	}
	if err := utgt.Del(context.Background(), DelOpts{DryRun: req.DryRun, AssumeYes: req.AssumeYes}); err != nil {
		return spec.DeployNodeDelDispatchReply{}, err
	}
	return spec.DeployNodeDelDispatchReply{}, nil
}

var _ = func() bool {
	registerHostBuilder(deployNodeDelDispatchBuilderKind, typedHostBuilder(deployNodeDelDispatchBuilderKind, hostBuildDeployNodeDelDispatch))
	return true
}()
