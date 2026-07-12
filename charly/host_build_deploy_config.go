package main

import (
	"context"
	"fmt"

	"github.com/opencharly/sdk/spec"
)

// host_build_deploy_config.go — the "deploy-config" F10 host-builder. The `charly bundle`
// CONFIG-MANAGEMENT subcommands (show/export/import/reset/status) moved to command:bundle
// (candy/plugin-bundle, P13); their handlers consult LoadUnified / the per-host deploy
// overlay (LoadBundleConfig / SaveBundleConfig / DeployConfigPath) — core Mechanisms a
// plugin (a separate module) cannot import. So the plugin's thin subcommands forward the
// Op + their authored inputs via HostBuild("deploy-config") and this builder runs the
// existing handler VERBATIM in-process (printing to the shared stdio). `path` is NOT here —
// it resolves via kit.DefaultDeployConfigPath entirely plugin-side, no seam. Generic action
// noun (F11); Op discriminates the subcommand.
const deployConfigBuilderKind = "deploy-config"

func hostBuildDeployConfig(_ context.Context, req spec.DeployConfigRequest, _ buildEngineContext) (spec.DeployConfigReply, error) {
	var err error
	switch req.Op {
	case "show":
		err = (&deployShowCmd{Box: req.Box, Instance: req.Instance}).Run()
	case "export":
		err = (&deployExportCmd{Boxes: req.Boxes, Output: req.Output, All: req.All}).Run()
	case "import":
		err = (&deployImportCmd{Files: req.Files, Replace: req.Replace, Box: req.Box}).Run()
	case "reset":
		err = (&deployResetCmd{Box: req.Box, Instance: req.Instance}).Run()
	case "status":
		err = (&deployStatusCmd{}).Run()
	default:
		return spec.DeployConfigReply{}, fmt.Errorf("deploy-config: unknown op %q", req.Op)
	}
	return spec.DeployConfigReply{}, err
}

var _ = func() bool {
	registerHostBuilder(deployConfigBuilderKind, typedHostBuilder(deployConfigBuilderKind, hostBuildDeployConfig))
	return true
}()
