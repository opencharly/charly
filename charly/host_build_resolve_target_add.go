package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/spec/spec"
)

// host_build_resolve_target_add.go — the "resolve-target-add" F10 host-builder (K4-C SHAPE-2
// keystone): the per-node TERMINAL step of `charly bundle add`, reached once per tree position from
// the plugin's own walk. It replaces the retired "deploy-node-dispatch" seam (host_build_deploy_
// node_dispatch.go) + its whole host-side compile half (the deleted bundle_compile_seam.go +
// deployAddCmd.dispatchNode/compileNodePlans): the plugin now COMPILES the InstallPlans IN-PROC and
// ships them ALREADY-COMPILED (PlansJSON — deployID + AddCandies stamped plugin-side), killing the
// former plugin→host→plugin double-bounce. This host half does ONLY the genuine floor-M residue a
// plugin (a separate module) cannot: reconstruct the ancestor executor chain (deriveChildExecutorFor
// Path — registry-coupled), loadConfigForDeploy (LoadUnified), and ResolveTarget + DeployContext +
// utgt.Add (the registry-coupled deploy dispatch anchor, floor-M per the deploy-dispatch ruling). A
// live DeployExecutor never crosses the wire — the plugin sends ancestor paths+nodes, the host
// re-derives the chain here.
const deployResolveTargetAddBuilderKind = "resolve-target-add"

func hostBuildResolveTargetAdd(_ context.Context, req spec.DeployResolveTargetAddRequest, _ buildEngineContext) (spec.DeployResolveTargetAddReply, error) {
	return spec.DeployResolveTargetAddReply{}, runResolveTargetAdd(req)
}

// reconstructParentExec re-derives the ancestor executor chain from the ROOT-FIRST ancestor
// path/node lists (deploykit.ResolveNodePath's own contract — EXCLUDING the target itself),
// mirroring the OLD in-core walk's ancestor-derivation loop. Relocated here from the deleted
// host_build_deploy_node_dispatch.go (unchanged); deriveChildExecutorForPath stays host-side in
// bundle_add_cmd.go (registry-coupled — deployTraitDescent needs the providerRegistry).
func reconstructParentExec(ancestorPaths []string, ancestorNodes []spec.BundleNode) (spec.DeployExecutor, error) {
	var parentExec spec.DeployExecutor
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

func runResolveTargetAdd(req spec.DeployResolveTargetAddRequest) error {
	parentExec, err := reconstructParentExec(req.AncestorPaths, req.AncestorNodes)
	if err != nil {
		return err
	}

	// Re-materialize the ALREADY-COMPILED plans (the plugin stamped deployID + AddCandies before
	// projecting to InstallPlanView, so the round-trip carries them intact).
	var views []spec.InstallPlanView
	if len(req.PlansJSON) > 0 {
		if err := json.Unmarshal(req.PlansJSON, &views); err != nil {
			return fmt.Errorf("resolve-target-add: decode compiled plans: %w", err)
		}
	}
	plans := make([]*spec.InstallPlan, 0, len(views))
	for _, v := range views {
		p, perr := spec.PlanFromView(v)
		if perr != nil {
			return fmt.Errorf("resolve-target-add: re-materialize plan: %w", perr)
		}
		plans = append(plans, p)
	}

	cfg, distroCfg, builderCfg, err := loadConfigForDeploy(req.Dir)
	if err != nil {
		return err
	}

	// Rebuild the FINAL resolved EmitOpts from the plugin-sent gate flags (node.InstallOpts already
	// applied over the CLI flags plugin-side); ParentExec/Path are the two live/local pieces filled
	// in HERE. DryRun never reaches this seam (a dry-run prints plugin-side and returns), so it is
	// absent from the request and left false.
	opts := spec.EmitOpts{
		AllowRepoChanges:     req.AllowRepoChanges,
		AllowRootTasks:       req.AllowRootTasks,
		WithServices:         req.WithServices,
		SkipIncompatible:     req.SkipIncompatible,
		AssumeYes:            req.AssumeYes,
		Verify:               req.Verify,
		Pull:                 req.Pull,
		BuilderImageOverride: req.BuilderImage,
		DevLocalPkg:          req.DevLocalPkg,
		ParentExec:           parentExec,
		Path:                 req.Path,
	}

	// ResolveTarget needs a node carrying target:. For a ref-based deploy with no charly.yml entry
	// (node == nil), synthesize one from the plugin-classified target so `charly bundle add host
	// ./x.yml` still resolves.
	resolveNode := req.Node
	if resolveNode == nil {
		resolveNode = &spec.BundleNode{Target: req.Target}
	}

	utgt, err := ResolveTarget(resolveNode, req.DeployName)
	if err != nil {
		return fmt.Errorf("resolve target: %w", err)
	}
	if tt, ok := utgt.(*pluginDeployTarget); ok {
		tt.nodeOnly = req.NodeOnly
	}

	dctx := &DeployContext{
		Node:       req.Node,
		Name:       req.DeployName,
		Dir:        req.Dir,
		Cfg:        cfg,
		DistroCfg:  distroCfg,
		BuilderCfg: builderCfg,
	}
	return utgt.Add(context.Background(), dctx, plans, opts)
}

var _ = func() bool {
	registerHostBuilder(deployResolveTargetAddBuilderKind, typedHostBuilder(deployResolveTargetAddBuilderKind, hostBuildResolveTargetAdd))
	return true
}()
