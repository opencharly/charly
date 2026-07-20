package main

import (
	"context"
	"fmt"

	"github.com/opencharly/sdk/spec"
)

// host_build_deploy_node_del_dispatch.go — the "deploy-node-del-dispatch" F10 host-builder
// (K4-C walk port): ResolveTarget + target.Del, honoring the teardown gates — the SAME terminal
// step the retired "deploy-dispatch" spike's Del leg ran. The live ReverseRunner is never carried
// on the wire — a programmatic teardown needing a specific runner is resolved host-side.
const deployNodeDelDispatchBuilderKind = "deploy-node-del-dispatch"

func hostBuildDeployNodeDelDispatch(_ context.Context, req spec.DeployNodeDelDispatchRequest, _ buildEngineContext) (spec.DeployNodeDelDispatchReply, error) {
	resolveNode := req.Node
	if resolveNode == nil {
		resolveNode = &spec.BundleNode{Target: "pod"}
	}

	utgt, err := ResolveTarget(resolveNode, req.Name)
	if err != nil {
		return spec.DeployNodeDelDispatchReply{}, fmt.Errorf("deploy-node-del-dispatch: resolve target: %w", err)
	}
	if tt, ok := utgt.(*externalDeployTarget); ok {
		// Mirrors deployDelCmd.Run's adapter setup — the CLI path never sets a revRunner
		// (test-only injection; always nil for a real `charly bundle del`).
		tt.KeepRepoChanges = req.KeepRepoChanges
		tt.KeepServices = req.KeepServices
		tt.KeepImage = req.KeepImage
	}

	if err := utgt.Del(context.Background(), DelOpts{
		DryRun:    req.DryRun,
		AssumeYes: req.AssumeYes,
	}); err != nil {
		return spec.DeployNodeDelDispatchReply{}, err
	}
	return spec.DeployNodeDelDispatchReply{}, nil
}

var _ = func() bool {
	registerHostBuilder(deployNodeDelDispatchBuilderKind, typedHostBuilder(deployNodeDelDispatchBuilderKind, hostBuildDeployNodeDelDispatch))
	return true
}()
