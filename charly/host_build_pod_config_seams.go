package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// host_build_pod_config_seams.go — the ~16 NARROW "pod-config-*" F10 host-builders the P13-KERNEL
// direction-flip introduces (sdk/schema/seam.cue). The former BoxConfigSetupCmd/BoxConfigRemoveCmd
// ORCHESTRATION (runConfig's sequencing, resolveDeployRef's dispatch, prepareQuadletEnv,
// parseVolumeFlags, persistResourceCaps' decision, runConfigDirect, directPodmanArgs,
// directDeployMarker*, checkMissingEnvRequires/checkMissingSecretRequires/warnMissingMCPRequires,
// updateAllDeployedQuadlets, BoxConfigRemoveCmd) MOVED to candy/plugin-deploy-pod
// (config_setup.go / config_remove.go, sdk.OpConfigSetup / sdk.OpConfigRemove). Each seam below
// wraps an EXISTING core function VERBATIM — unchanged internally — for the pieces that are
// genuinely host/loader/registry/credential-coupled (the ledger's registered FINAL/K5 IOU family
// for credential-store/enc.go internals; the DeployStateHost nil-seam for loader access; the
// embedded (via a Go embed directive) sidecar template data that lives only in the charly binary).

const (
	podConfigEnsureImageKind      = "pod-config-ensure-image"
	podConfigResolveRefKind       = "pod-config-resolve-ref"
	podConfigLoadDeployKind       = "pod-config-load-deploy"
	podConfigSaveBundleKind       = "pod-config-save-bundle"
	podConfigLoadBundleKind       = "pod-config-load-bundle"
	podConfigMigrateSecretsKind   = "pod-config-migrate-secrets"
	podConfigScrubCliEnvKind      = "pod-config-scrub-cli-env"
	podConfigDetectDevicesKind    = "pod-config-detect-devices"
	podConfigTunnelResolveKind    = "pod-config-tunnel-resolve"
	podConfigResolveSidecarsKind  = "pod-config-resolve-sidecars"
	podConfigProvisionSecretsKind = "pod-config-provision-secrets"
	podConfigEncMountsKind        = "pod-config-enc-mounts"
	podConfigInjectEnvKind        = "pod-config-inject-env-provides"
	podConfigInjectMCPKind        = "pod-config-inject-mcp-provides"
	podConfigHookSecretEnvKind    = "pod-config-hook-secret-env"
	podConfigSSHKeyKind           = "pod-config-ssh-key"
	podConfigListSidecarsKind     = "pod-config-list-sidecars"
	podConfigEncEnsurePlanKind    = "pod-config-enc-ensure-plan"
	podConfigEncUnmountPlanKind   = "pod-config-enc-unmount-plan"
	podConfigContainerTunnelKind  = "pod-config-container-tunnel"
	podConfigBoxEngineKind        = "pod-config-box-engine"
	podConfigCleanDeployEntryKind = "pod-config-clean-deploy-entry"
	podConfigProjectVolumeKind    = "pod-config-project-volume"
)

func hostBuildPodConfigListSidecars(_ context.Context, _ spec.PodConfigLoadDeployRequest, _ buildEngineContext) (spec.PodConfigListSidecarsReply, error) {
	templates, err := embeddedSidecarBodies()
	if err != nil {
		return spec.PodConfigListSidecarsReply{}, err
	}
	names := make([]string, 0, len(templates))
	descriptions := make(map[string]string, len(templates))
	for name, body := range templates {
		names = append(names, name)
		var meta struct {
			Description string `json:"description"`
		}
		_ = json.Unmarshal(body, &meta)
		descriptions[name] = meta.Description
	}
	return spec.PodConfigListSidecarsReply{Names: names, Descriptions: descriptions}, nil
}

func hostBuildPodConfigSSHKey(_ context.Context, req spec.PodConfigSSHKeyRequest, _ buildEngineContext) (spec.PodConfigSSHKeyReply, error) {
	if req.Flag == "" {
		return spec.PodConfigSSHKeyReply{}, nil
	}
	sshDir, err := containerSSHKeyDir(req.ContainerName)
	if err != nil {
		return spec.PodConfigSSHKeyReply{}, err
	}
	pubkey, err := resolveSSHPubKey(req.Flag, sshDir)
	if err != nil {
		return spec.PodConfigSSHKeyReply{}, fmt.Errorf("resolving SSH key: %w", err)
	}
	return spec.PodConfigSSHKeyReply{Pubkey: pubkey}, nil
}

func hostBuildPodConfigEnsureImage(_ context.Context, req spec.PodConfigEnsureImageRequest, _ buildEngineContext) (spec.PodConfigEnsureImageReply, error) {
	podmanRT := &kit.ResolvedRuntime{BuildEngine: req.BuildEngine, RunEngine: "podman"}
	if err := EnsureImage(req.ImageRef, podmanRT); err != nil {
		return spec.PodConfigEnsureImageReply{}, err
	}
	meta, err := deploykit.ExtractMetadata("podman", req.ImageRef)
	if err != nil {
		return spec.PodConfigEnsureImageReply{}, err
	}
	if meta == nil {
		return spec.PodConfigEnsureImageReply{}, fmt.Errorf("image %s has no embedded metadata; rebuild with latest charly", req.ImageRef)
	}
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return spec.PodConfigEnsureImageReply{}, err
	}
	return spec.PodConfigEnsureImageReply{MetaJSON: metaJSON}, nil
}

func hostBuildPodConfigResolveRef(_ context.Context, req spec.PodConfigResolveRefRequest, _ buildEngineContext) (spec.PodConfigResolveRefReply, error) {
	if req.ExplicitRef != "" {
		return spec.PodConfigResolveRefReply{DeployBoxName: req.ExplicitRef, ImageRef: req.ExplicitRef}, nil
	}
	deployBoxName := resolveDeployBoxName(req.Box, req.Instance)
	imageRef := ""
	if ov := resolveDeployResolvedImage(req.Box, req.Instance); ov != "" && kit.LocalImageExists("podman", ov) {
		imageRef = ov
	} else {
		imageRef = kit.ResolveShellImageRef("", deployBoxName, req.Tag)
	}
	return spec.PodConfigResolveRefReply{DeployBoxName: deployBoxName, ImageRef: imageRef}, nil
}

func hostBuildPodConfigLoadDeploy(_ context.Context, req spec.PodConfigLoadDeployRequest, _ buildEngineContext) (spec.PodConfigLoadDeployReply, error) {
	dc := deploykit.LoadDeployConfigForRead(req.Caller)
	if dc == nil {
		return spec.PodConfigLoadDeployReply{}, nil
	}
	b, err := json.Marshal(dc)
	if err != nil {
		return spec.PodConfigLoadDeployReply{}, err
	}
	return spec.PodConfigLoadDeployReply{ConfigJSON: b}, nil
}

// hostBuildPodConfigProjectVolume resolves req.Box/req.Instance's PROJECT-declared `volume:`
// override via the SAME merged project+operator tree `charly bundle add` walks
// (resolveTreeRoot, deploy_tree.go) — never the per-host overlay alone. Scoped to ONE deploy key
// so it stays a narrow, single-purpose read: it does not perturb any other Setup logic that reads
// the bare per-host overlay via #PodConfigLoadDeployRequest.
func hostBuildPodConfigProjectVolume(_ context.Context, req spec.PodConfigProjectVolumeRequest, _ buildEngineContext) (spec.PodConfigProjectVolumeReply, error) {
	dir, err := os.Getwd()
	if err != nil {
		return spec.PodConfigProjectVolumeReply{}, err
	}
	tree, err := resolveTreeRoot(dir)
	if err != nil {
		return spec.PodConfigProjectVolumeReply{}, err
	}
	node, ok := tree[deploykit.DeployKey(req.Box, req.Instance)]
	if !ok || len(node.Volume) == 0 {
		return spec.PodConfigProjectVolumeReply{}, nil
	}
	b, err := json.Marshal(node.Volume)
	if err != nil {
		return spec.PodConfigProjectVolumeReply{}, err
	}
	return spec.PodConfigProjectVolumeReply{VolumeJSON: b}, nil
}

func hostBuildPodConfigSaveBundle(_ context.Context, req spec.PodConfigSaveBundleRequest, _ buildEngineContext) (spec.PodConfigSaveBundleReply, error) {
	var dc deploykit.BundleConfig
	if err := json.Unmarshal(req.ConfigJSON, &dc); err != nil {
		return spec.PodConfigSaveBundleReply{}, err
	}
	return spec.PodConfigSaveBundleReply{}, saveBundleConfigNodeForm(&dc)
}

func hostBuildPodConfigLoadBundle(_ context.Context, _ spec.PodConfigLoadDeployRequest, _ buildEngineContext) (spec.PodConfigLoadBundleReply, error) {
	dc, err := deploykit.LoadBundleConfig()
	if err != nil || dc == nil {
		return spec.PodConfigLoadBundleReply{}, err
	}
	b, err := json.Marshal(dc)
	if err != nil {
		return spec.PodConfigLoadBundleReply{}, err
	}
	return spec.PodConfigLoadBundleReply{ConfigJSON: b}, nil
}

func hostBuildPodConfigMigrateSecrets(_ context.Context, req spec.PodConfigMigrateSecretsRequest, _ buildEngineContext) (spec.PodConfigMigrateSecretsReply, error) {
	var dc deploykit.BundleConfig
	if err := json.Unmarshal(req.ConfigJSON, &dc); err != nil {
		return spec.PodConfigMigrateSecretsReply{}, err
	}
	var meta spec.BoxMetadata
	if err := json.Unmarshal(req.MetaJSON, &meta); err != nil {
		return spec.PodConfigMigrateSecretsReply{}, err
	}
	migrated, err := MigratePlaintextEnvSecret(&dc, &meta, req.Box, req.Instance)
	if err != nil {
		return spec.PodConfigMigrateSecretsReply{}, err
	}
	b, merr := json.Marshal(&dc)
	if merr != nil {
		return spec.PodConfigMigrateSecretsReply{}, merr
	}
	return spec.PodConfigMigrateSecretsReply{ConfigJSON: b, Migrated: migrated}, nil
}

func hostBuildPodConfigScrubCliEnv(_ context.Context, req spec.PodConfigScrubCliEnvRequest, _ buildEngineContext) (spec.PodConfigScrubCliEnvReply, error) {
	var meta spec.BoxMetadata
	if err := json.Unmarshal(req.MetaJSON, &meta); err != nil {
		return spec.PodConfigScrubCliEnvReply{}, err
	}
	cleaned, imported := scrubSecretCLIEnv(req.CliEnv, &meta)
	return spec.PodConfigScrubCliEnvReply{Cleaned: cleaned, Imported: imported}, nil
}

func hostBuildPodConfigDetectDevices(_ context.Context, req spec.PodConfigDetectDevicesRequest, _ buildEngineContext) (spec.PodConfigDetectDevicesReply, error) {
	var detected DetectedDevices
	if !req.NoAutoDetect {
		detected = DetectHostDevices()
		LogDetectedDevices(detected)
	}
	if detected.GPU && req.Engine == "podman" {
		EnsureCDI()
	}
	b, err := json.Marshal(detected)
	if err != nil {
		return spec.PodConfigDetectDevicesReply{}, err
	}
	return spec.PodConfigDetectDevicesReply{DetectedJSON: b}, nil
}

func hostBuildPodConfigEncEnsurePlan(_ context.Context, req spec.PodConfigEncEnsurePlanRequest, _ buildEngineContext) (spec.PodConfigEncEnsurePlanReply, error) {
	body, err := resolvePodEncEnsure(req.Box, req.Instance)
	if err != nil {
		return spec.PodConfigEncEnsurePlanReply{}, err
	}
	return spec.PodConfigEncEnsurePlanReply{EncJSON: body}, nil
}

func hostBuildPodConfigEncUnmountPlan(_ context.Context, req spec.PodConfigEncUnmountPlanRequest, _ buildEngineContext) (spec.PodConfigEncUnmountPlanReply, error) {
	body, err := resolvePodEncUnmount(req.Box, req.Instance)
	if err != nil {
		return spec.PodConfigEncUnmountPlanReply{}, err
	}
	return spec.PodConfigEncUnmountPlanReply{EncJSON: body}, nil
}

func hostBuildPodConfigContainerTunnel(_ context.Context, req spec.PodConfigContainerTunnelRequest, _ buildEngineContext) (spec.PodConfigContainerTunnelReply, error) {
	tc := resolvePodTunnel(req.Box, req.Instance)
	if tc == nil {
		return spec.PodConfigContainerTunnelReply{}, nil
	}
	b, err := json.Marshal(tc)
	if err != nil {
		return spec.PodConfigContainerTunnelReply{}, err
	}
	return spec.PodConfigContainerTunnelReply{TunnelJSON: b}, nil
}

func hostBuildPodConfigBoxEngine(_ context.Context, req spec.PodConfigBoxEngineRequest, _ buildEngineContext) (spec.PodConfigBoxEngineReply, error) {
	return spec.PodConfigBoxEngineReply{Engine: deploykit.ResolveBoxEngineForDeploy(req.Box, req.Instance, req.GlobalEngine)}, nil
}

func hostBuildPodConfigTunnelResolve(_ context.Context, req spec.PodConfigTunnelResolveRequest, _ buildEngineContext) (spec.PodConfigTunnelResolveReply, error) {
	var meta spec.BoxMetadata
	if err := json.Unmarshal(req.MetaJSON, &meta); err != nil {
		return spec.PodConfigTunnelResolveReply{}, err
	}
	if meta.Tunnel == nil {
		return spec.PodConfigTunnelResolveReply{}, nil
	}
	tc := deploykit.TunnelConfigFromMetadata(&meta)
	b, err := json.Marshal(tc)
	if err != nil {
		return spec.PodConfigTunnelResolveReply{}, err
	}
	return spec.PodConfigTunnelResolveReply{TunnelJSON: b}, nil
}

// hostBuildPodConfigResolveSidecars wraps the former BoxConfigSetupCmd.resolveSidecars body
// VERBATIM (embeddedSidecarBodies' go:embed data lives only in the charly binary; the plugin
// dispatch + sidecar-secret provisioning are registry/credential-coupled per the same FINAL/K5
// family enc.go documents).
func hostBuildPodConfigResolveSidecars(_ context.Context, req spec.PodConfigResolveSidecarsRequest, _ buildEngineContext) (spec.PodConfigResolveSidecarsReply, error) {
	var deploySidecars map[string]json.RawMessage
	if len(req.DeploySidecarsJSON) > 0 {
		if err := json.Unmarshal(req.DeploySidecarsJSON, &deploySidecars); err != nil {
			return spec.PodConfigResolveSidecarsReply{}, err
		}
	}
	if len(deploySidecars) == 0 {
		return spec.PodConfigResolveSidecarsReply{AppEnv: req.CliEnv}, nil
	}
	var projectTemplates map[string]json.RawMessage
	if len(req.ProjectTemplatesJSON) > 0 {
		if err := json.Unmarshal(req.ProjectTemplatesJSON, &projectTemplates); err != nil {
			return spec.PodConfigResolveSidecarsReply{}, err
		}
	}
	embedded, err := embeddedSidecarBodies()
	if err != nil {
		return spec.PodConfigResolveSidecarsReply{}, fmt.Errorf("resolving sidecars: %w", err)
	}
	reply, err := resolveSidecarsViaPlugin(spec.SidecarResolveInput{
		EmbeddedTemplates: embedded,
		ProjectTemplates:  projectTemplates,
		DeployOverrides:   deploySidecars,
		CliEnv:            req.CliEnv,
		Box:               req.Box,
		Instance:          req.Instance,
	})
	if err != nil {
		return spec.PodConfigResolveSidecarsReply{}, fmt.Errorf("resolving sidecars: %w", err)
	}
	appEnv := reply.AppEnv
	resolvedSidecars := make([]deploykit.ResolvedSidecar, 0, len(reply.Sidecars))
	for _, rs := range reply.Sidecars {
		resolvedSidecars = append(resolvedSidecars, resolvedSidecarFromSpec(rs))
	}

	var extraEnv []string
	for i, sc := range resolvedSidecars {
		if len(sc.Secret) == 0 {
			continue
		}
		scSecrets, _ := deploykit.ApplySecretRefresh(sc.Secret, req.RefreshSecret)
		scProvisioned, scFallback, scErr := ProvisionPodmanSecrets(req.RunEngine, req.Box, req.Instance, scSecrets, req.AutoGen)
		if scErr != nil {
			continue // best-effort — mirrors the former in-Run() Warning-only handling
		}
		resolvedSidecars[i].Secret = scProvisioned
		extraEnv = append(extraEnv, scFallback...)
	}

	persistJSON, err := json.Marshal(reply.PersistOverrides)
	if err != nil {
		return spec.PodConfigResolveSidecarsReply{}, err
	}
	resolvedJSON, err := json.Marshal(resolvedSidecars)
	if err != nil {
		return spec.PodConfigResolveSidecarsReply{}, err
	}
	return spec.PodConfigResolveSidecarsReply{
		PersistOverridesJSON: persistJSON,
		ResolvedSidecarsJSON: resolvedJSON,
		AppEnv:               appEnv,
		ExtraEnv:             extraEnv,
	}, nil
}

func hostBuildPodConfigProvisionSecrets(_ context.Context, req spec.PodConfigProvisionSecretsRequest, _ buildEngineContext) (spec.PodConfigProvisionSecretsReply, error) {
	var meta spec.BoxMetadata
	if err := json.Unmarshal(req.MetaJSON, &meta); err != nil {
		return spec.PodConfigProvisionSecretsReply{}, err
	}
	candyOwnedSecrets := deploykit.CollectSecretsFromLabels(req.Box, meta.Secret)
	credBackedSecrets, secretResolutions := CollectCandySecretAccepts(req.Box, req.Instance, &meta)
	collectedSecrets := append(append([]deploykit.CollectedSecret{}, candyOwnedSecrets...), credBackedSecrets...)
	collectedSecrets, _ = deploykit.ApplySecretRefresh(collectedSecrets, req.RefreshSecret)
	provisioned, fallbackEnv, err := ProvisionPodmanSecrets(req.RunEngine, req.Box, req.Instance, collectedSecrets, req.AutoGen)
	if err != nil {
		return spec.PodConfigProvisionSecretsReply{}, err
	}
	provisionedJSON, err := json.Marshal(provisioned)
	if err != nil {
		return spec.PodConfigProvisionSecretsReply{}, err
	}
	resolutionsJSON, err := json.Marshal(secretResolutions)
	if err != nil {
		return spec.PodConfigProvisionSecretsReply{}, err
	}
	backend := resolveSecretBackend()
	isKeyring := backend == "keyring" || backend == "auto" || backend == ""
	return spec.PodConfigProvisionSecretsReply{
		ProvisionedJSON: provisionedJSON,
		FallbackEnv:     fallbackEnv,
		ResolutionsJSON: resolutionsJSON,
		IsKeyring:       isKeyring,
	}, nil
}

func hostBuildPodConfigEncMounts(_ context.Context, req spec.PodConfigEncMountsRequest, _ buildEngineContext) (spec.PodConfigEncMountsReply, error) {
	if err := ensureEncryptedMounts(req.Box, req.Instance, req.AutoGen); err != nil {
		return spec.PodConfigEncMountsReply{}, fmt.Errorf("setting up encrypted volumes: %w", err)
	}
	if !req.KeepMounted {
		if err := encUnmount(req.Box, req.Instance, ""); err != nil {
			return spec.PodConfigEncMountsReply{}, fmt.Errorf("unmounting encrypted volumes: %w", err)
		}
	}
	return spec.PodConfigEncMountsReply{}, nil
}

func hostBuildPodConfigInjectEnv(_ context.Context, req spec.PodConfigInjectEnvProvidesRequest, _ buildEngineContext) (spec.PodConfigInjectEnvProvidesReply, error) {
	var portMap map[int]int
	if len(req.PortMapJSON) > 0 {
		if err := json.Unmarshal(req.PortMapJSON, &portMap); err != nil {
			return spec.PodConfigInjectEnvProvidesReply{}, err
		}
	}
	changed, err := injectEnvProvides(req.Box, req.Instance, req.EnvProvides, portMap)
	if err != nil {
		return spec.PodConfigInjectEnvProvidesReply{}, err
	}
	return spec.PodConfigInjectEnvProvidesReply{Changed: changed}, nil
}

func hostBuildPodConfigInjectMCP(_ context.Context, req spec.PodConfigInjectMCPProvidesRequest, _ buildEngineContext) (spec.PodConfigInjectMCPProvidesReply, error) {
	var mcpProvides []spec.MCPServerYAML
	if len(req.MCPProvidesJSON) > 0 {
		if err := json.Unmarshal(req.MCPProvidesJSON, &mcpProvides); err != nil {
			return spec.PodConfigInjectMCPProvidesReply{}, err
		}
	}
	var portMap map[int]int
	if len(req.PortMapJSON) > 0 {
		if err := json.Unmarshal(req.PortMapJSON, &portMap); err != nil {
			return spec.PodConfigInjectMCPProvidesReply{}, err
		}
	}
	changed, err := injectMCPProvides(req.Box, req.Instance, mcpProvides, portMap)
	if err != nil {
		return spec.PodConfigInjectMCPProvidesReply{}, err
	}
	return spec.PodConfigInjectMCPProvidesReply{Changed: changed}, nil
}

func hostBuildPodConfigHookSecretEnv(_ context.Context, req spec.PodConfigHookSecretEnvRequest, _ buildEngineContext) (spec.PodConfigHookSecretEnvReply, error) {
	var meta spec.BoxMetadata
	if err := json.Unmarshal(req.MetaJSON, &meta); err != nil {
		return spec.PodConfigHookSecretEnvReply{}, err
	}
	return spec.PodConfigHookSecretEnvReply{Env: resolveHookSecretEnv(req.Box, req.Instance, &meta)}, nil
}

// hostBuildPodConfigCleanDeployEntry wraps deploykit.CleanDeployEntry VERBATIM (Cutover B unit 2
// remove-verb completion) — the registry-resugar axis of `charly remove`'s deploy-entry cleanup.
// marshalDeployNode needs the host's plugin-primaries registry to resugar plan steps (the SAME
// K4-exit family CleanDeployEntry's own callers document), so this narrow twin of
// hostBuildDeployConfigSaveState (host_build_deploy_config_save_state.go) stays host-side rather
// than forcing the wrong-shaped deploy-config-save seam to fit (see
// #PodConfigCleanDeployEntryRequest's doc comment).
func hostBuildPodConfigCleanDeployEntry(_ context.Context, req spec.PodConfigCleanDeployEntryRequest, _ buildEngineContext) (spec.PodConfigCleanDeployEntryReply, error) {
	deploykit.CleanDeployEntry(req.Box, req.Instance, marshalDeployNode)
	return spec.PodConfigCleanDeployEntryReply{}, nil
}

var _ = func() bool {
	registerHostBuilder(podConfigEnsureImageKind, typedHostBuilder(podConfigEnsureImageKind, hostBuildPodConfigEnsureImage))
	registerHostBuilder(podConfigResolveRefKind, typedHostBuilder(podConfigResolveRefKind, hostBuildPodConfigResolveRef))
	registerHostBuilder(podConfigLoadDeployKind, typedHostBuilder(podConfigLoadDeployKind, hostBuildPodConfigLoadDeploy))
	registerHostBuilder(podConfigSaveBundleKind, typedHostBuilder(podConfigSaveBundleKind, hostBuildPodConfigSaveBundle))
	registerHostBuilder(podConfigLoadBundleKind, typedHostBuilder(podConfigLoadBundleKind, hostBuildPodConfigLoadBundle))
	registerHostBuilder(podConfigMigrateSecretsKind, typedHostBuilder(podConfigMigrateSecretsKind, hostBuildPodConfigMigrateSecrets))
	registerHostBuilder(podConfigScrubCliEnvKind, typedHostBuilder(podConfigScrubCliEnvKind, hostBuildPodConfigScrubCliEnv))
	registerHostBuilder(podConfigDetectDevicesKind, typedHostBuilder(podConfigDetectDevicesKind, hostBuildPodConfigDetectDevices))
	registerHostBuilder(podConfigTunnelResolveKind, typedHostBuilder(podConfigTunnelResolveKind, hostBuildPodConfigTunnelResolve))
	registerHostBuilder(podConfigResolveSidecarsKind, typedHostBuilder(podConfigResolveSidecarsKind, hostBuildPodConfigResolveSidecars))
	registerHostBuilder(podConfigProvisionSecretsKind, typedHostBuilder(podConfigProvisionSecretsKind, hostBuildPodConfigProvisionSecrets))
	registerHostBuilder(podConfigEncMountsKind, typedHostBuilder(podConfigEncMountsKind, hostBuildPodConfigEncMounts))
	registerHostBuilder(podConfigInjectEnvKind, typedHostBuilder(podConfigInjectEnvKind, hostBuildPodConfigInjectEnv))
	registerHostBuilder(podConfigInjectMCPKind, typedHostBuilder(podConfigInjectMCPKind, hostBuildPodConfigInjectMCP))
	registerHostBuilder(podConfigHookSecretEnvKind, typedHostBuilder(podConfigHookSecretEnvKind, hostBuildPodConfigHookSecretEnv))
	registerHostBuilder(podConfigSSHKeyKind, typedHostBuilder(podConfigSSHKeyKind, hostBuildPodConfigSSHKey))
	registerHostBuilder(podConfigListSidecarsKind, typedHostBuilder(podConfigListSidecarsKind, hostBuildPodConfigListSidecars))
	registerHostBuilder(podConfigEncEnsurePlanKind, typedHostBuilder(podConfigEncEnsurePlanKind, hostBuildPodConfigEncEnsurePlan))
	registerHostBuilder(podConfigEncUnmountPlanKind, typedHostBuilder(podConfigEncUnmountPlanKind, hostBuildPodConfigEncUnmountPlan))
	registerHostBuilder(podConfigContainerTunnelKind, typedHostBuilder(podConfigContainerTunnelKind, hostBuildPodConfigContainerTunnel))
	registerHostBuilder(podConfigBoxEngineKind, typedHostBuilder(podConfigBoxEngineKind, hostBuildPodConfigBoxEngine))
	registerHostBuilder(podConfigCleanDeployEntryKind, typedHostBuilder(podConfigCleanDeployEntryKind, hostBuildPodConfigCleanDeployEntry))
	registerHostBuilder(podConfigProjectVolumeKind, typedHostBuilder(podConfigProjectVolumeKind, hostBuildPodConfigProjectVolume))
	return true
}()
