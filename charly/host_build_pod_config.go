package main

import (
	"context"

	"github.com/opencharly/sdk/spec"
)

// host_build_pod_config.go — the "pod-config-*" F10 host-builders. The `charly config …`
// command's CLI GRAMMAR moved to command:config (candy/plugin-pod, the DEPLOY wave), but the
// implementation structs — BoxConfigSetupCmd/BoxConfigStatusCmd/BoxConfigMountCmd/
// BoxConfigUnmountCmd/BoxConfigPasswdCmd/BoxConfigRemoveCmd (config_image.go) — STAY CORE
// UNCHANGED, by their EXACT unchanged names: BoxConfigSetupCmd is ALSO constructed directly (not
// through the CLI) by bundle_from_box_cmd.go and host_build_deploy_from_box.go (both P13-kernel,
// out of this wave's scope), so it cannot be renamed or moved without stranding them. Each
// plugin leaf forwards its flags via its own HostBuild("pod-config-<leaf>") seam, and each
// builder here reconstructs the UNCHANGED core struct and runs its Run() body VERBATIM.
const (
	podConfigSetupBuilderKind   = "pod-config-setup"
	podConfigStatusBuilderKind  = "pod-config-status"
	podConfigMountBuilderKind   = "pod-config-mount"
	podConfigUnmountBuilderKind = "pod-config-unmount"
	podConfigPasswdBuilderKind  = "pod-config-passwd"
	podConfigRemoveBuilderKind  = "pod-config-remove"
)

func hostBuildPodConfigSetup(_ context.Context, req spec.PodConfigSetupRequest, _ buildEngineContext) (spec.PodConfigSetupReply, error) {
	cmd := BoxConfigSetupCmd{
		Box:           req.Box,
		Tag:           req.Tag,
		Build:         req.Build,
		Env:           req.Env,
		Clean:         req.Clean,
		EnvFile:       req.EnvFile,
		Instance:      req.Instance,
		Port:          req.Port,
		KeepMounted:   req.KeepMounted,
		Password:      req.Password,
		RefreshSecret: req.RefreshSecret,
		VolumeFlag:    req.VolumeFlag,
		Bind:          req.Bind,
		Encrypt:       req.Encrypt,
		MemoryMax:     req.MemoryMax,
		MemoryHigh:    req.MemoryHigh,
		MemorySwapMax: req.MemorySwapMax,
		Cpus:          req.Cpus,
		Seed:          req.Seed,
		ForceSeed:     req.ForceSeed,
		DataFrom:      req.DataFrom,
		UpdateAll:     req.UpdateAll,
		SshKey:        req.SshKey,
		Sidecar:       req.Sidecar,
		ListSidecars:  req.ListSidecars,
		AutoDetectFlags: AutoDetectFlags{
			NoAutoDetect: req.NoAutoDetect,
		},
	}
	return spec.PodConfigSetupReply{}, cmd.Run()
}

func hostBuildPodConfigStatus(_ context.Context, req spec.PodConfigStatusRequest, _ buildEngineContext) (spec.PodConfigStatusReply, error) {
	cmd := BoxConfigStatusCmd{Box: req.Box, Instance: req.Instance}
	return spec.PodConfigStatusReply{}, cmd.Run()
}

func hostBuildPodConfigMount(_ context.Context, req spec.PodConfigMountRequest, _ buildEngineContext) (spec.PodConfigMountReply, error) {
	cmd := BoxConfigMountCmd{Box: req.Box, Volume: req.Volume, Instance: req.Instance}
	return spec.PodConfigMountReply{}, cmd.Run()
}

func hostBuildPodConfigUnmount(_ context.Context, req spec.PodConfigUnmountRequest, _ buildEngineContext) (spec.PodConfigUnmountReply, error) {
	cmd := BoxConfigUnmountCmd{Box: req.Box, Volume: req.Volume, Instance: req.Instance}
	return spec.PodConfigUnmountReply{}, cmd.Run()
}

func hostBuildPodConfigPasswd(_ context.Context, req spec.PodConfigPasswdRequest, _ buildEngineContext) (spec.PodConfigPasswdReply, error) {
	cmd := BoxConfigPasswdCmd{Box: req.Box, Instance: req.Instance}
	return spec.PodConfigPasswdReply{}, cmd.Run()
}

func hostBuildPodConfigRemove(_ context.Context, req spec.PodConfigRemoveRequest, _ buildEngineContext) (spec.PodConfigRemoveReply, error) {
	cmd := BoxConfigRemoveCmd{Box: req.Box, Instance: req.Instance}
	return spec.PodConfigRemoveReply{}, cmd.Run()
}

var _ = func() bool {
	registerHostBuilder(podConfigSetupBuilderKind, typedHostBuilder(podConfigSetupBuilderKind, hostBuildPodConfigSetup))
	registerHostBuilder(podConfigStatusBuilderKind, typedHostBuilder(podConfigStatusBuilderKind, hostBuildPodConfigStatus))
	registerHostBuilder(podConfigMountBuilderKind, typedHostBuilder(podConfigMountBuilderKind, hostBuildPodConfigMount))
	registerHostBuilder(podConfigUnmountBuilderKind, typedHostBuilder(podConfigUnmountBuilderKind, hostBuildPodConfigUnmount))
	registerHostBuilder(podConfigPasswdBuilderKind, typedHostBuilder(podConfigPasswdBuilderKind, hostBuildPodConfigPasswd))
	registerHostBuilder(podConfigRemoveBuilderKind, typedHostBuilder(podConfigRemoveBuilderKind, hostBuildPodConfigRemove))
	return true
}()
