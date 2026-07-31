package deploypod

// config_seam_kinds.go — the "pod-config-*" HostBuild kind names, matching
// charly/host_build_pod_config_seams.go's registrations exactly (R3: one name list, two sides).
// The migrate-secrets + scrub-cli-env kinds were shed in #55 coneC Unit C4 (the logic relocated
// plugin-side to secret_migration.go — no HostBuild round-trip), so they no longer appear here.
const (
	podConfigEnsureImageKind    = "pod-config-ensure-image"
	podConfigLoadDeployKind     = "pod-config-load-deploy"
	podConfigSaveBundleKind     = "pod-config-save-bundle"
	podConfigLoadBundleKind     = "pod-config-load-bundle"
	podConfigDetectDevicesKind  = "pod-config-detect-devices"
	podConfigTunnelResolveKind  = "pod-config-tunnel-resolve"
	podConfigSSHKeyKind         = "pod-config-ssh-key"
	podConfigListSidecarsKind   = "pod-config-list-sidecars"
	podConfigBoxEngineKind     = "pod-config-box-engine"
)
