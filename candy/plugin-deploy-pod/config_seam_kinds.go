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
	podConfigHookSecretEnvKind    = "pod-config-hook-secret-env"
	podConfigSSHKeyKind           = "pod-config-ssh-key"
	podConfigListSidecarsKind     = "pod-config-list-sidecars"
	podConfigBoxEngineKind        = "pod-config-box-engine"
)

// deployConfigSaveStateKind is the substrate-neutral "deploy-config-save-state" HostBuild kind
// (S3b, Q2 — was "pod-config-save-deploy-state" here too, until candy/plugin-bundle's generic
// Add/Update apply body became a caller across every substrate). Kept in this same const list
// (mirrors charly/host_build_deploy_config_save_state.go's registration) even though the name no
// longer starts with "pod-config-" — the two original callers below are still in this candy.
const deployConfigSaveStateKind = "deploy-config-save-state"
