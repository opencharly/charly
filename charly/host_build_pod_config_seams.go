package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/spec"
	"github.com/opencharly/spec/sshx"
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
	podConfigLoadDeployKind       = "pod-config-load-deploy"
	podConfigSaveBundleKind       = "pod-config-save-bundle"
	podConfigDetectDevicesKind    = "pod-config-detect-devices"
	podConfigTunnelResolveKind    = "pod-config-tunnel-resolve"
	podConfigSSHKeyKind           = "pod-config-ssh-key"
	podConfigListSidecarsKind     = "pod-config-list-sidecars"
	podConfigBoxEngineKind        = "pod-config-box-engine"
	podConfigContainerTunnelKind  = "pod-config-container-tunnel"
	podConfigCleanDeployEntryKind = "pod-config-clean-deploy-entry"
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

func hostBuildPodConfigEnsureImage(_ context.Context, req spec.PodConfigEnsureImageRequest, _ buildEngineContext) (spec.PodConfigEnsureImageReply, error) {
	podmanRT := &kit.ResolvedRuntime{BuildEngine: req.BuildEngine, RunEngine: "podman"}
	if err := ensureImagePresent(req.ImageRef, podmanRT); err != nil {
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
// Returns kit.ErrImageNotLocal (wrapped with the ref) only when ALL three tiers fail.
func ensureImagePresent(imageRef string, rt *kit.ResolvedRuntime) error {
	if kit.LocalImageExists(rt.RunEngine, imageRef) {
		return nil
	}
	// Cross-engine transfer first when applicable: faster than a network pull and works offline.
	if rt.BuildEngine != rt.RunEngine && kit.LocalImageExists(rt.BuildEngine, imageRef) {
		return kit.TransferImage(rt.BuildEngine, rt.RunEngine, imageRef)
	}
	// Generic ensure: pull, fall back to local build for project images.
	if err := dispatchBuildEnsure(context.Background(), imageRef, "", rt.BuildEngine, rt.RunEngine); err == nil {
		return nil
	}
	return fmt.Errorf("%w: %s", kit.ErrImageNotLocal, imageRef)
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

func hostBuildPodConfigSaveBundle(_ context.Context, req spec.PodConfigSaveBundleRequest, _ buildEngineContext) (spec.PodConfigSaveBundleReply, error) {
	var dc deploykit.BundleConfig
	if err := json.Unmarshal(req.ConfigJSON, &dc); err != nil {
		return spec.PodConfigSaveBundleReply{}, err
	}
	return spec.PodConfigSaveBundleReply{}, saveBundleConfigNodeForm(&dc)
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

func hostBuildPodConfigBoxEngine(_ context.Context, req spec.PodConfigBoxEngineRequest, _ buildEngineContext) (spec.PodConfigBoxEngineReply, error) {
	return spec.PodConfigBoxEngineReply{Engine: deploykit.ResolveBoxEngineForDeploy(req.Box, req.Instance, req.GlobalEngine)}, nil
}

// hostBuildPodConfigContainerTunnel resolves the tunnel config (charly.yml-only; labels never
// carry tunnel) a caller starts/stops/tears down. STILL core-resident (wave γ narrowed its
// candy/plugin-deploy-pod start/stop callers to their own local resolvePodTunnelPlan —
// enc_tunnel_resolve.go — but candy/plugin-pod's `charly remove` teardown path
// (remove_tunnel.go) is a SEPARATE caller of this SAME seam, so it stays registered). Reads the
// RUNNING container's baked image ref (registry/podman-store coupled — genuinely host-only).
func hostBuildPodConfigContainerTunnel(_ context.Context, req spec.PodConfigContainerTunnelRequest, _ buildEngineContext) (spec.PodConfigContainerTunnelReply, error) {
	ctrName := kit.ContainerNameInstance(req.Box, req.Instance)
	imageRef := kit.ContainerImage("podman", ctrName)
	if imageRef == "" {
		return spec.PodConfigContainerTunnelReply{}, nil
	}
	meta, err := deploykit.ExtractMetadata("podman", imageRef)
	if err != nil || meta == nil {
		return spec.PodConfigContainerTunnelReply{}, nil
	}
	dc := deploykit.LoadDeployConfigForRead("charly tunnel resolve")
	deploykit.MergeDeployOntoMetadata(meta, dc, req.Box, req.Instance)
	if meta.Tunnel == nil {
		return spec.PodConfigContainerTunnelReply{}, nil
	}
	tc := deploykit.TunnelConfigFromMetadata(meta)
	b, err := json.Marshal(tc)
	if err != nil {
		return spec.PodConfigContainerTunnelReply{}, err
	}
	return spec.PodConfigContainerTunnelReply{TunnelJSON: b}, nil
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

// hostBuildPodConfigCleanDeployEntry wraps deploykit.CleanDeployEntry VERBATIM (Cutover B unit 2
// remove-verb completion) — the registry-resugar axis of `charly remove`'s deploy-entry cleanup.
// marshalDeployNode needs the host's plugin-primaries registry to resugar plan steps, so this seam
// stays host-side, wrapping deploykit.CleanDeployEntry with the host marshalDeployNode (see
// #PodConfigCleanDeployEntryRequest's doc comment). Its own plugin-side collapse — sourcing
// Primaries via the generic loader-threaded leg like the #55 K4 deploy-state writes already do — is
// a pod-config-seam follow-on cone, not this unit.
func hostBuildPodConfigCleanDeployEntry(_ context.Context, req spec.PodConfigCleanDeployEntryRequest, _ buildEngineContext) (spec.PodConfigCleanDeployEntryReply, error) {
	deploykit.CleanDeployEntry(req.Box, req.Instance, marshalDeployNode)
	return spec.PodConfigCleanDeployEntryReply{}, nil
}

var _ = func() bool {
	registerHostBuilder(podConfigEnsureImageKind, typedHostBuilder(podConfigEnsureImageKind, hostBuildPodConfigEnsureImage))
	registerHostBuilder(podConfigLoadDeployKind, typedHostBuilder(podConfigLoadDeployKind, hostBuildPodConfigLoadDeploy))
	registerHostBuilder(podConfigSaveBundleKind, typedHostBuilder(podConfigSaveBundleKind, hostBuildPodConfigSaveBundle))
	registerHostBuilder(podConfigDetectDevicesKind, typedHostBuilder(podConfigDetectDevicesKind, hostBuildPodConfigDetectDevices))
	registerHostBuilder(podConfigTunnelResolveKind, typedHostBuilder(podConfigTunnelResolveKind, hostBuildPodConfigTunnelResolve))
	registerHostBuilder(podConfigSSHKeyKind, typedHostBuilder(podConfigSSHKeyKind, hostBuildPodConfigSSHKey))
	registerHostBuilder(podConfigListSidecarsKind, typedHostBuilder(podConfigListSidecarsKind, hostBuildPodConfigListSidecars))
	registerHostBuilder(podConfigBoxEngineKind, typedHostBuilder(podConfigBoxEngineKind, hostBuildPodConfigBoxEngine))
	registerHostBuilder(podConfigContainerTunnelKind, typedHostBuilder(podConfigContainerTunnelKind, hostBuildPodConfigContainerTunnel))
	registerHostBuilder(podConfigCleanDeployEntryKind, typedHostBuilder(podConfigCleanDeployEntryKind, hostBuildPodConfigCleanDeployEntry))
	return true
}()
