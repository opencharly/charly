package main

import (
	"context"
	"encoding/json"

	"github.com/opencharly/spec/spec"
)

// host_build_pod_config_seams.go — the NARROW "pod-config-*" F10 host-builders that REMAIN host-side
// after the #55 coneC-dsh pod-config seam-collapse + K-wave W3a B6's per-leg death: the device
// detection (DetectHostDevices/EnsureCDI) and the embedded sidecar template bodies (go:embed, live
// only in the charly binary). The six deploykit-only legs (pod-config-load-deploy / -save-bundle /
// -box-engine / -tunnel-resolve / -container-tunnel / -clean-deploy-entry) are DELETED —
// candy/plugin-deploy-pod + candy/plugin-pod now call deploykit/loaderkit directly
// (loaderkit.LoadHostBundleConfigViaExecutor for the overlay read, fetchLoaderPrimaries/
// deployMarshalNode for the node-form marshal, deploykit.ExtractMetadata / TunnelConfigFromMetadata
// / CleanDeployEntry / SaveBundleConfig for the bodies), shedding this file's sdk/deploykit import.
// The pod-config-ensure-image and pod-config-ssh-key legs DIED (K-wave W3a B6, unit A2's proven
// peer-dispatch pattern): candy/plugin-deploy-pod now drives podman/build:ensure and reads the
// host SSH-key FS ITSELF (image_ensure.go/sshkey_resolve.go there) — spec/container and spec/sshx
// were already fully portable, so this was a pure caller-update, no new mechanism. The remaining
// two legs stay host-side because their data (the device-detection tables shared with `charly
// doctor`; the embedded charly.yml a separate Go module cannot go:embed) is genuinely core-only —
// see config_seam_kinds.go (candy/plugin-deploy-pod) for the per-leg death-vs-stay rationale. The
// former BoxConfigSetupCmd/BoxConfigRemoveCmd ORCHESTRATION moved to candy/plugin-deploy-pod
// (config_setup.go / config_remove.go) in P13-KERNEL.

const (
	podConfigDetectDevicesKind = "pod-config-detect-devices"
	podConfigListSidecarsKind  = "pod-config-list-sidecars"
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
	// BodiesJSON carries the FULL go:embed bodies (the only host-resident piece) so
	// candy/plugin-deploy-pod's sidecar_resolve.go can InvokeProvider kind:sidecar itself (the
	// resolve seam-death) — the introspection Names/Descriptions serve `charly config --list-sidecars`.
	bodiesJSON, err := json.Marshal(templates)
	if err != nil {
		return spec.PodConfigListSidecarsReply{}, err
	}
	return spec.PodConfigListSidecarsReply{Names: names, Descriptions: descriptions, BodiesJSON: bodiesJSON}, nil
}

func hostBuildPodConfigDetectDevices(_ context.Context, req spec.PodConfigDetectDevicesRequest, _ buildEngineContext) (spec.PodConfigDetectDevicesReply, error) {
	var detected spec.DetectedDevices
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

var _ = func() bool {
	registerHostBuilder(podConfigDetectDevicesKind, typedHostBuilder(podConfigDetectDevicesKind, hostBuildPodConfigDetectDevices))
	registerHostBuilder(podConfigListSidecarsKind, typedHostBuilder(podConfigListSidecarsKind, hostBuildPodConfigListSidecars))
	return true
}()
