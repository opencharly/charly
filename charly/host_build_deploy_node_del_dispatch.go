package main

import (
	"context"
	"fmt"

	"github.com/opencharly/sdk/spec"
	"github.com/opencharly/sdk/vmshared"
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

	// RCA #9 (FINAL/K5 unit 6a, live-probe-caught): "vm:" is a CLI ADDRESSING hint ("resolve
	// via the vm substrate"), never an identity — strip it (vmshared.SplitVmAddress) BEFORE it becomes
	// the deploy target's internal name. Left unstripped, the target's name carries the raw
	// "vm:"-prefixed CLI form, so deploykit.ComputeDeployID(name, nil, nil) (a bare SHA256 of
	// name) never matches the hash the ADD-time tree walk computed from the plain form — a
	// SILENT no-op teardown: the Del path's ledger lookup (now candy/plugin-bundle's
	// handleDeployDel, S3b) finds no record under the mismatched ID and takes its "nothing
	// recorded — idempotent teardown" branch, which is correct for a GENUINELY already-removed
	// deploy but wrong here (verified live: the two forms hash to completely different IDs,
	// 6413f8070aaa6087 vs d81fff596411fea4, for the exact same logical deployment).
	name, _ := vmshared.SplitVmAddress(req.Name)

	utgt, err := ResolveTarget(resolveNode, name)
	if err != nil {
		return spec.DeployNodeDelDispatchReply{}, fmt.Errorf("deploy-node-del-dispatch: resolve target: %w", err)
	}
	if tt, ok := utgt.(*pluginDeployTarget); ok {
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
