package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/spec/container"
	"github.com/opencharly/spec/hostenv"
	"github.com/opencharly/spec/spec"
	"github.com/opencharly/spec/sshx"
)

// host_build_pod_config_seams.go — the NARROW "pod-config-*" F10 host-builders that REMAIN host-side
// after the #55 coneC-dsh pod-config seam-collapse: the pieces that are genuinely host/loader/embed-
// coupled — the image-ensure (podman store + dispatchBuildEnsure), the device detection
// (DetectHostDevices/EnsureCDI), the embedded sidecar template bodies (go:embed, live only in the
// charly binary), and the SSH-key resolution. The six deploykit-only legs
// (pod-config-load-deploy / -save-bundle / -box-engine / -tunnel-resolve / -container-tunnel /
// -clean-deploy-entry) are DELETED — candy/plugin-deploy-pod + candy/plugin-pod now call
// deploykit/loaderkit directly (loaderkit.LoadHostBundleConfigViaExecutor for the overlay read,
// fetchLoaderPrimaries/deployMarshalNode for the node-form marshal, deploykit.ExtractMetadata /
// TunnelConfigFromMetadata / CleanDeployEntry / SaveBundleConfig for the bodies), shedding this
// file's sdk/deploykit import. The pod-config-ensure-image leg SHRANK: the host leg keeps the
// host-coupled ensureImagePresent (podman store + build:ensure dispatch) and the plugin calls
// deploykit.ExtractMetadata itself. The former BoxConfigSetupCmd/BoxConfigRemoveCmd ORCHESTRATION
// moved to candy/plugin-deploy-pod (config_setup.go / config_remove.go) in P13-KERNEL.

const (
	podConfigEnsureImageKind   = "pod-config-ensure-image"
	podConfigDetectDevicesKind = "pod-config-detect-devices"
	podConfigSSHKeyKind        = "pod-config-ssh-key"
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

func hostBuildPodConfigSSHKey(_ context.Context, req spec.PodConfigSSHKeyRequest, _ buildEngineContext) (spec.PodConfigSSHKeyReply, error) {
	if req.Flag == "" {
		return spec.PodConfigSSHKeyReply{}, nil
	}
	sshDir, err := sshx.ContainerSSHKeyDir(req.ContainerName)
	if err != nil {
		return spec.PodConfigSSHKeyReply{}, err
	}
	pubkey, err := sshx.ResolveSSHPubKey(req.Flag, sshDir)
	if err != nil {
		return spec.PodConfigSSHKeyReply{}, fmt.Errorf("resolving SSH key: %w", err)
	}
	return spec.PodConfigSSHKeyReply{Pubkey: pubkey}, nil
}

// hostBuildPodConfigEnsureImage is the HOST-COUPLED half of pod-config-ensure-image (#55 coneC-dsh
// seam-collapse): it guarantees imageRef is present in the run engine's local store (ensureImagePresent
// — podman store + dispatchBuildEnsure). The deploykit.ExtractMetadata label extract that USED to
// run here is now done PLUGIN-SIDE by candy/plugin-deploy-pod / candy/plugin-pod (the host leg
// returns an empty reply; the plugin calls deploykit.ExtractMetadata itself), shedding this file's
// deploykit import. The plugin passes nil for the reply destination (hostBuild's rep==nil path).
func hostBuildPodConfigEnsureImage(_ context.Context, req spec.PodConfigEnsureImageRequest, _ buildEngineContext) (spec.PodConfigEnsureImageReply, error) {
	podmanRT := &hostenv.ResolvedRuntime{BuildEngine: req.BuildEngine, RunEngine: "podman"}
	if err := ensureImagePresent(req.ImageRef, podmanRT); err != nil {
		return spec.PodConfigEnsureImageReply{}, err
	}
	return spec.PodConfigEnsureImageReply{}, nil
}

// ensureImagePresent guarantees imageRef is available in the run engine's local store — the
// deploy-cone image-ensure glue folded here from the retired transfer.go (its own header
// classified it UNTIL-K4/deploy-cone, and its sole consumer was this pod-config-ensure-image
// seam). Three-tier fallback (each step independent):
//
//  1. Already-present short-circuit (LocalImageExists in run engine).
//  2. Cross-engine transfer (`docker save | podman load`) when build engine != run engine AND
//     the image is present in the build engine's storage.
//  3. dispatchBuildEnsure — the compiled-in candy/plugin-build build:ensure word: pulls from the
//     registry, falling back to a local `charly box build <name>` when the ref maps to a project
//     charly.yml entry (the SAME code path BuilderRun, the check preflight, and `charly box pull`
//     all go through — see charly/dispatch_build_ensure.go).
//
// Returns spec.ErrImageNotLocal (wrapped with the ref) only when ALL three tiers fail.
func ensureImagePresent(imageRef string, rt *hostenv.ResolvedRuntime) error {
	if container.LocalImageExists(rt.RunEngine, imageRef) {
		return nil
	}
	// Cross-engine transfer first when applicable: faster than a network pull and works offline.
	if rt.BuildEngine != rt.RunEngine && container.LocalImageExists(rt.BuildEngine, imageRef) {
		return container.TransferImage(rt.BuildEngine, rt.RunEngine, imageRef)
	}
	// Generic ensure: pull, fall back to local build for project images.
	if err := dispatchBuildEnsure(context.Background(), imageRef, "", rt.BuildEngine, rt.RunEngine); err == nil {
		return nil
	}
	return fmt.Errorf("%w: %s", spec.ErrImageNotLocal, imageRef)
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
	registerHostBuilder(podConfigEnsureImageKind, typedHostBuilder(podConfigEnsureImageKind, hostBuildPodConfigEnsureImage))
	registerHostBuilder(podConfigDetectDevicesKind, typedHostBuilder(podConfigDetectDevicesKind, hostBuildPodConfigDetectDevices))
	registerHostBuilder(podConfigSSHKeyKind, typedHostBuilder(podConfigSSHKeyKind, hostBuildPodConfigSSHKey))
	registerHostBuilder(podConfigListSidecarsKind, typedHostBuilder(podConfigListSidecarsKind, hostBuildPodConfigListSidecars))
	return true
}()
