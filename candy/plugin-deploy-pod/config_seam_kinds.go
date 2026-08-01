package deploypod

// config_seam_kinds.go — the "pod-config-*" HostBuild kind names that REMAIN registered host-side
// after the #55 coneC-dsh pod-config seam-collapse, matching charly/host_build_pod_config_seams.go's
// registrations exactly (R3: one name list, two sides). The six deploykit-only legs
// (pod-config-load-deploy / -save-bundle / -box-engine / -tunnel-resolve / -container-tunnel /
// -clean-deploy-entry) are DELETED — candy/plugin-deploy-pod + candy/plugin-pod now call
// deploykit/loaderkit directly, so their kind consts are gone from both sides. The
// migrate-secrets + scrub-cli-env kinds were shed earlier in #55 coneC Unit C4.
const (
	podConfigEnsureImageKind   = "pod-config-ensure-image"
	podConfigDetectDevicesKind = "pod-config-detect-devices"
	podConfigSSHKeyKind        = "pod-config-ssh-key"
	podConfigListSidecarsKind  = "pod-config-list-sidecars"
)
