package main

import (
	"context"

	"github.com/opencharly/sdk/spec"
)

// host_build_deploy_add.go — the "deploy-add" F10 host-builder. The `charly bundle add`
// command moved to command:bundle (candy/plugin-bundle, P13), but the deploy KERNEL it
// drives — the config loader, the InstallPlan compiler, ResolveTarget →
// externalDeployTarget, and the live-executor composition (host objects that cannot cross
// the process boundary) — STAYS CORE, exactly as the box-build engine stayed core behind
// HostBuild("image") in P8 and the VM-disk engine behind HostBuild("vm-build") in P10. So
// core keeps the whole deployAddCmd orchestration; the plugin's thin `bundle add` command
// forwards its flags via HostBuild("deploy-add") and this builder runs the existing add
// orchestration VERBATIM (Run → dispatchNode → compile → ResolveTarget → Add) in-process
// (the plugin is compiled-in, so dry-run + progress flow to the shared stdout/stderr).
// Generic action noun (F11 — never a substrate word).
const deployAddBuilderKind = "deploy-add"

func hostBuildDeployAdd(_ context.Context, req spec.DeployAddRequest, _ buildEngineContext) (spec.DeployAddReply, error) {
	cmd := deployAddCmd{
		Name:             req.Name,
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
	if cmd.Format == "" {
		cmd.Format = "table" // the former Kong `default:"table"` on --format (the plugin defaults it too)
	}
	return spec.DeployAddReply{}, cmd.Run()
}

var _ = func() bool {
	registerHostBuilder(deployAddBuilderKind, typedHostBuilder(deployAddBuilderKind, hostBuildDeployAdd))
	return true
}()
