package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

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
	podConfigResolveRefKind       = "pod-config-resolve-ref"
	podConfigLoadDeployKind       = "pod-config-load-deploy"
	podConfigSaveBundleKind       = "pod-config-save-bundle"
	podConfigLoadBundleKind       = "pod-config-load-bundle"
	podConfigMigrateSecretsKind   = "pod-config-migrate-secrets"
	podConfigScrubCliEnvKind      = "pod-config-scrub-cli-env"
	podConfigDetectDevicesKind    = "pod-config-detect-devices"
	podConfigTunnelResolveKind    = "pod-config-tunnel-resolve"
	podConfigSSHKeyKind           = "pod-config-ssh-key"
	podConfigListSidecarsKind     = "pod-config-list-sidecars"
	podConfigBoxEngineKind        = "pod-config-box-engine"
	podConfigContainerTunnelKind  = "pod-config-container-tunnel"
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
	node, ok := tree[spec.DeployKey(req.Box, req.Instance)]
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
	registerHostBuilder(podConfigSSHKeyKind, typedHostBuilder(podConfigSSHKeyKind, hostBuildPodConfigSSHKey))
	registerHostBuilder(podConfigListSidecarsKind, typedHostBuilder(podConfigListSidecarsKind, hostBuildPodConfigListSidecars))
	registerHostBuilder(podConfigBoxEngineKind, typedHostBuilder(podConfigBoxEngineKind, hostBuildPodConfigBoxEngine))
	registerHostBuilder(podConfigContainerTunnelKind, typedHostBuilder(podConfigContainerTunnelKind, hostBuildPodConfigContainerTunnel))
	registerHostBuilder(podConfigCleanDeployEntryKind, typedHostBuilder(podConfigCleanDeployEntryKind, hostBuildPodConfigCleanDeployEntry))
	registerHostBuilder(podConfigProjectVolumeKind, typedHostBuilder(podConfigProjectVolumeKind, hostBuildPodConfigProjectVolume))
	return true
}()
