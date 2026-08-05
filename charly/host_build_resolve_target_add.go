package main

import (
	"context"
	"fmt"

	"github.com/opencharly/spec/spec"
)

// host_build_resolve_target_add.go — the "resolve-target-add" F10 host-builder (K4-C SHAPE-2
// keystone): the per-node TERMINAL step of `charly bundle add`. The plugin ships ALREADY-COMPILED
// plans; this host half does ONLY the floor-M residue a plugin cannot: reconstruct the ancestor
// executor chain (spec.ReconstructParentExec over the registry-coupled deriveChildExecutorForPath),
// loadConfigForDeploy (LoadUnified), and ResolveTarget + utgt.Add. Plan re-materialization +
// EmitOpts + the ancestor loop are spec helpers (K-wave 2 cone R2 bank D thin).
const deployResolveTargetAddBuilderKind = "resolve-target-add"

func hostBuildResolveTargetAdd(_ context.Context, req spec.DeployResolveTargetAddRequest, _ buildEngineContext) (spec.DeployResolveTargetAddReply, error) {
	return spec.DeployResolveTargetAddReply{}, runResolveTargetAdd(req)
}

func runResolveTargetAdd(req spec.DeployResolveTargetAddRequest) error {
	parentExec, err := spec.ReconstructParentExec(req.AncestorPaths, req.AncestorNodes, deriveChildExecutorForPath)
	if err != nil {
		return err
	}
	plans, err := spec.PlansFromViews(req.PlansJSON)
	if err != nil {
		return fmt.Errorf("resolve-target-add: %w", err)
	}
	cfg, distroCfg, builderCfg, err := loadConfigForDeploy(req.Dir)
	if err != nil {
		return err
	}
	opts := spec.EmitOptsFromResolveTargetAdd(req, parentExec)
	if req.Node == nil {
		req.Node = &spec.BundleNode{Target: req.Target} // ref-based deploy: synthesize from the plugin-classified target
	}
	utgt, err := ResolveTarget(req.Node, req.DeployName)
	if err != nil {
		return fmt.Errorf("resolve target: %w", err)
	}
	if tt, ok := utgt.(*pluginDeployTarget); ok {
		tt.nodeOnly = req.NodeOnly
	}
	return utgt.Add(context.Background(), &DeployContext{Node: req.Node, Name: req.DeployName, Dir: req.Dir, Cfg: cfg, DistroCfg: distroCfg, BuilderCfg: builderCfg}, plans, opts)
}

var _ = func() bool {
	registerHostBuilder(deployResolveTargetAddBuilderKind, typedHostBuilder(deployResolveTargetAddBuilderKind, hostBuildResolveTargetAdd))
	return true
}()
