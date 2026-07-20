package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// host_build_deploy_config_save.go — the "deploy-config-save" F10 host-builder (K4-C). The
// `charly bundle` config-management subcommands (show/export/import/reset/status) moved OUT of
// charly core into command:bundle (candy/plugin-bundle), calling deploykit.LoadBundleConfig /
// ExportAllBox / ParseDeployKey etc. DIRECTLY (already sdk-portable) and the EXISTING
// HostBuild("resolved-project") seam for export's project-load touch — only the deploy-state
// SAVE step (import/reset) still needs a seam: saveBundleConfigNodeForm's per-entry marshal
// callback (marshalBundleNode, deploy_nodeform.go) resugars each plan step via the host-owned
// pluginPrimaries registry, which a separate-module plugin cannot reach. Generic action noun
// (F11 — never a substrate word).
const deployConfigSaveBuilderKind = "deploy-config-save"

func hostBuildDeployConfigSave(_ context.Context, req spec.DeployConfigSaveRequest, _ buildEngineContext) (spec.DeployConfigSaveReply, error) {
	var dc deploykit.BundleConfig
	if len(req.ConfigJSON) == 0 {
		return spec.DeployConfigSaveReply{}, fmt.Errorf("deploy-config-save: empty config")
	}
	if err := json.Unmarshal(req.ConfigJSON, &dc); err != nil {
		return spec.DeployConfigSaveReply{}, fmt.Errorf("deploy-config-save: decode config: %w", err)
	}
	if err := saveBundleConfigNodeForm(&dc); err != nil {
		return spec.DeployConfigSaveReply{}, err
	}
	return spec.DeployConfigSaveReply{}, nil
}

var _ = func() bool {
	registerHostBuilder(deployConfigSaveBuilderKind, typedHostBuilder(deployConfigSaveBuilderKind, hostBuildDeployConfigSave))
	return true
}()
