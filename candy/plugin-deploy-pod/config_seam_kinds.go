package deploypod

// config_seam_kinds.go — the "pod-config-*" HostBuild kind names that REMAIN registered host-side
// after the #55 coneC-dsh pod-config seam-collapse, matching charly/host_build_pod_config_seams.go's
// registrations exactly (R3: one name list, two sides). The six deploykit-only legs
// (pod-config-load-deploy / -save-bundle / -box-engine / -tunnel-resolve / -container-tunnel /
// -clean-deploy-entry) are DELETED — candy/plugin-deploy-pod + candy/plugin-pod now call
// deploykit/loaderkit directly, so their kind consts are gone from both sides. The
// migrate-secrets + scrub-cli-env kinds were shed earlier in #55 coneC Unit C4. The
// pod-config-ensure-image and pod-config-ssh-key legs DIED (K-wave W3a B6): the plugin drives
// podman/build:ensure and reads the host SSH-key FS itself now (image_ensure.go,
// sshkey_resolve.go) — spec/container and spec/sshx were already portable, no seam needed.
// pod-config-detect-devices and pod-config-list-sidecars REMAIN registered — the former needs
// core-embedded detection tables shared with charly doctor (an IOU, not a clean single-owner
// relocation); the latter needs an RDD spike before its go:embed relocation (the SAME embedded
// data also feeds the generic loader-level applyEmbeddedDefaults merge — untangling the two
// consumers needs a live bed proof before moving, not assumed here).
const (
	podConfigDetectDevicesKind = "pod-config-detect-devices"
	podConfigListSidecarsKind  = "pod-config-list-sidecars"
)
