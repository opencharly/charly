package main

import (
	"context"
	"encoding/json"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// host_build_deploy_config_save_state.go — the "deploy-config-save-state" F10 host-builder.
// Renamed substrate-neutral (S3b, Q2 — was "pod-config-save-deploy-state" in
// host_build_pod_config_seams.go): the seam started as a pod-only per-deploy state persist
// (candy/plugin-deploy-pod's config_setup.go/resolve.go, still its two original callers), but
// candy/plugin-bundle's generic Add/Update apply body (deploy_target.go's persistDeployState)
// now calls it for EVERY substrate (pod/vm/local/k8s/android) whose PrepareVenue reply carries a
// State patch, so the "pod-config-*" family naming no longer fit the seam's actual scope — only
// the kind string + Go identifiers moved; deploykit.SaveDeployState's behavior is unchanged
// (marshalDeployNode still resugars each plan step via the host-owned pluginPrimaries registry,
// unreachable from a separate-module plugin — the reason this stays a HostBuild seam at all).
//
// MIGRATION INVENTORY: this file's `deploykit` import (SaveDeployStateInput +
// SaveDeployState) is UNTIL-K4 — it exits with the K4 deploy-state family externalization
// (deploy + config resolution moving to sdk/deploykit + the deploy/bundle plugins), at which
// point marshalDeployNode's pluginPrimaries dependency either moves plugin-side too or this
// whole HostBuild seam collapses into a direct plugin-side call.
const deployConfigSaveStateKind = "deploy-config-save-state"

func hostBuildDeployConfigSaveState(_ context.Context, req spec.DeployConfigSaveStateRequest, _ buildEngineContext) (spec.DeployConfigSaveStateReply, error) {
	var input deploykit.SaveDeployStateInput
	if err := json.Unmarshal(req.InputJSON, &input); err != nil {
		return spec.DeployConfigSaveStateReply{}, err
	}
	deploykit.SaveDeployState(req.Box, req.Instance, input, marshalDeployNode)
	return spec.DeployConfigSaveStateReply{}, nil
}

var _ = func() bool {
	registerHostBuilder(deployConfigSaveStateKind, typedHostBuilder(deployConfigSaveStateKind, hostBuildDeployConfigSaveState))
	return true
}()
