package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// host_build_deploy_dispatch.go — the "deploy-dispatch" F10 host-builder (K4-C: P13-KERNEL spike
// #1 add + the del increment). Reached from candy/plugin-bundle's OpDispatch relay (dispatch.go):
// resolves the live UnifiedDeployTarget via the SAME ResolveTarget the host-initiated path uses,
// and runs Add/Del — the one piece of the deploy kernel that genuinely cannot leave the host
// process (a live provider-registry handle bound to THIS process's broker + a live Executor).
// "test"/"update" are reserved (spec.DeployDispatchRequest.Op enum) but have NO current caller
// anywhere in the codebase (UnifiedDeployTarget.Test/Update are unimplemented-anywhere interface
// methods — no `charly check live`/`charly bundle update` command exists yet), so they error
// loudly rather than serve unreachable code. Generic action noun (F11 — never a substrate word),
// mirroring "deploy-add"/"resolved-project".
const deployDispatchBuilderKind = "deploy-dispatch"

func hostBuildDeployDispatch(ctx context.Context, req spec.DeployDispatchRequest, build buildEngineContext) (spec.DeployDispatchReply, error) {
	switch req.Op {
	case "add":
		return hostBuildDeployDispatchAdd(ctx, req, build)
	case "del":
		return hostBuildDeployDispatchDel(ctx, req, build)
	default:
		return spec.DeployDispatchReply{}, fmt.Errorf("deploy-dispatch: op %q not yet wired (test/update have no caller today)", req.Op)
	}
}

// hostBuildDeployDispatchAdd reconstructs the project config (loadConfigForDeploy —
// core-loader-coupled, K1-blocked, never marshaled), re-materializes the compiled InstallPlans
// from their wire views, and runs ResolveTarget(...).Add(...).
func hostBuildDeployDispatchAdd(_ context.Context, req spec.DeployDispatchRequest, _ buildEngineContext) (spec.DeployDispatchReply, error) {
	cfg, distroCfg, builderCfg, err := loadConfigForDeploy(req.Dir)
	if err != nil {
		return spec.DeployDispatchReply{}, fmt.Errorf("deploy-dispatch: load config: %w", err)
	}

	var plans []*deploykit.InstallPlan
	if len(req.PlansJSON) > 0 {
		var views []spec.InstallPlanView
		if err := json.Unmarshal(req.PlansJSON, &views); err != nil {
			return spec.DeployDispatchReply{}, fmt.Errorf("deploy-dispatch: decode plans: %w", err)
		}
		plans = make([]*deploykit.InstallPlan, 0, len(views))
		for _, v := range views {
			p, err := deploykit.PlanFromView(v)
			if err != nil {
				return spec.DeployDispatchReply{}, fmt.Errorf("deploy-dispatch: rematerialize plan: %w", err)
			}
			plans = append(plans, p)
		}
	}

	resolveNode := req.Node
	if resolveNode == nil {
		resolveNode = &spec.BundleNode{Target: req.Target}
	}

	utgt, err := ResolveTarget(resolveNode, req.DeployName)
	if err != nil {
		return spec.DeployDispatchReply{}, fmt.Errorf("deploy-dispatch: resolve target: %w", err)
	}
	if tt, ok := utgt.(*externalDeployTarget); ok {
		tt.nodeOnly = req.NodeOnly
	}

	dctx := &DeployContext{
		Node:       req.Node,
		Name:       req.DeployName,
		Dir:        req.Dir,
		Cfg:        cfg,
		DistroCfg:  distroCfg,
		BuilderCfg: builderCfg,
		Base:       req.Base,
	}

	opts := deploykit.EmitOpts{
		DryRun:               req.DryRun,
		FormatJSON:           req.FormatJSON,
		AllowRepoChanges:     req.AllowRepoChanges,
		AllowRootTasks:       req.AllowRootTasks,
		WithServices:         req.WithServices,
		SkipIncompatible:     req.SkipIncompatible,
		AssumeYes:            req.AssumeYes,
		Verify:               req.Verify,
		Pull:                 req.Pull,
		BuilderImageOverride: req.BuilderImageOverride,
	}

	if err := utgt.Add(context.Background(), dctx, plans, opts); err != nil {
		return spec.DeployDispatchReply{}, err
	}
	return spec.DeployDispatchReply{}, nil
}

// hostBuildDeployDispatchDel resolves the live UnifiedDeployTarget and runs
// ResolveTarget(...).Del(...) — no DeployContext, no plans, no nested-executor concept
// (deployDelCmd.Run never threads a ParentExec), so every del dispatch reaches here.
func hostBuildDeployDispatchDel(_ context.Context, req spec.DeployDispatchRequest, _ buildEngineContext) (spec.DeployDispatchReply, error) {
	resolveNode := req.Node
	if resolveNode == nil {
		resolveNode = &spec.BundleNode{Target: req.Target}
	}

	utgt, err := ResolveTarget(resolveNode, req.DeployName)
	if err != nil {
		return spec.DeployDispatchReply{}, fmt.Errorf("deploy-dispatch: resolve target: %w", err)
	}
	if tt, ok := utgt.(*externalDeployTarget); ok {
		// Mirrors deployDelCmd.Run's adapter setup — the CLI path never sets revRunner
		// (test-only injection; always nil for a real `charly bundle del`).
		tt.KeepRepoChanges = req.KeepRepoChanges
		tt.KeepServices = req.KeepServices
		tt.KeepImage = req.KeepImage
	}

	if err := utgt.Del(context.Background(), DelOpts{
		DryRun:        req.DryRun,
		AssumeYes:     req.AssumeYes,
		KeepLedger:    req.KeepLedger,
		RemoveVolumes: req.RemoveVolumes,
	}); err != nil {
		return spec.DeployDispatchReply{}, err
	}
	return spec.DeployDispatchReply{}, nil
}

var _ = func() bool {
	registerHostBuilder(deployDispatchBuilderKind, typedHostBuilder(deployDispatchBuilderKind, hostBuildDeployDispatch))
	return true
}()
