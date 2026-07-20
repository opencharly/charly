package main

import (
	"context"
	"fmt"
	"os"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// host_build_deploy_node_dispatch.go — the "deploy-node-dispatch" F10 host-builder (K4-C walk
// port keystone): resolve+compile+ResolveTarget+Add for ONE tree position, reached once per node
// from the plugin's own walk instead of core walking in-process. AncestorPaths/AncestorNodes let
// the host reconstruct the SAME parentExec chain the OLD in-core walk built —
// deriveChildExecutorForPath is pure Go over spec/kit types and is re-run HOST-side here — so a
// live DeployExecutor never needs to cross the wire.
const deployNodeDispatchBuilderKind = "deploy-node-dispatch"

func hostBuildDeployNodeDispatch(_ context.Context, req spec.DeployNodeDispatchRequest, _ buildEngineContext) (spec.DeployNodeDispatchReply, error) {
	return spec.DeployNodeDispatchReply{}, runDeployNodeDispatch(req)
}

// reconstructParentExec re-derives the ancestor executor chain from the ROOT-FIRST
// ancestor path/node lists (deploykit.ResolveNodePath's own contract — EXCLUDING the target
// itself), mirroring the OLD Run()'s own ancestor-derivation loop.
func reconstructParentExec(ancestorPaths []string, ancestorNodes []spec.BundleNode) (deploykit.DeployExecutor, error) {
	var parentExec deploykit.DeployExecutor
	for i, ap := range ancestorPaths {
		var anc *spec.BundleNode
		if i < len(ancestorNodes) {
			anc = &ancestorNodes[i]
		}
		next, err := deriveChildExecutorForPath(ap, anc, parentExec)
		if err != nil {
			return nil, fmt.Errorf("deriving executor for ancestor %q: %w", ap, err)
		}
		parentExec = next
	}
	return parentExec, nil
}

func runDeployNodeDispatch(req spec.DeployNodeDispatchRequest) error {
	dir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}

	parentExec, err := reconstructParentExec(req.AncestorPaths, req.AncestorNodes)
	if err != nil {
		return err
	}

	c := &deployAddCmd{
		Name:             req.Path,
		Ref:              req.Ref,
		AddCandy:         req.AddCandy,
		Tag:              req.Tag,
		DryRun:           req.DryRun,
		NodeOnly:         req.NodeOnly,
		Format:           req.Format,
		Pull:             req.Pull,
		Verify:           req.Verify,
		WithServices:     req.WithServices,
		AllowRepoChanges: req.AllowRepoChanges,
		AllowRootTasks:   req.AllowRootTasks,
		SkipIncompatible: req.SkipIncompatible,
		BuilderImage:     req.BuilderImage,
		AssumeYes:        req.AssumeYes,
		Disposable:       req.Disposable,
		Lifecycle:        req.Lifecycle,
	}
	if c.Format == "" {
		c.Format = "table"
	}

	return c.dispatchNode(req.Path, req.Node, parentExec, dir)
}

var _ = func() bool {
	registerHostBuilder(deployNodeDispatchBuilderKind, typedHostBuilder(deployNodeDispatchBuilderKind, hostBuildDeployNodeDispatch))
	return true
}()
