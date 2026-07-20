package deploypod

// config_seam_kinds.go — the "pod-config-*" HostBuild kind names, matching
// charly/host_build_pod_config_seams.go's registrations exactly (R3: one name list, two sides).
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
	podConfigSaveDeployStateKind  = "pod-config-save-deploy-state"
	podConfigHookSecretEnvKind    = "pod-config-hook-secret-env"
	podConfigSSHKeyKind           = "pod-config-ssh-key"
	podConfigListSidecarsKind     = "pod-config-list-sidecars"
	podConfigEncEnsurePlanKind    = "pod-config-enc-ensure-plan"
	podConfigEncUnmountPlanKind   = "pod-config-enc-unmount-plan"
	podConfigContainerTunnelKind  = "pod-config-container-tunnel"
	podConfigBoxEngineKind        = "pod-config-box-engine"
)
