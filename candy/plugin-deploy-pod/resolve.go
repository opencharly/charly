package deploypod

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// resolve.go — the P13-KERNEL step-4(ii) direction-flip: the pod START/STOP plan resolution
// (formerly charly-core's pod_lifecycle_resolve.go resolvePodStartPlan/resolvePodStartQuadlet/
// resolvePodStartDirect/resolvePodStopPlan/resolvePodRuntimeImage) moved here VERBATIM — same
// sequencing, same argv-building logic (byte-diff gated against the pre-move source). The plugin
// self-resolves using #PodStartOpts/#PodStopOpts + the deploy key (box/instance, threaded via
// lifecycleParams.Name), calling back the narrow "pod-config-*" seams for host/loader/registry-
// coupled sub-steps (image ensure, device detection, deploy-config load, box-engine lookup, the
// enc-ensure/enc-unmount/container-tunnel credential/registry-coupled bundles).

// resolvePodStartPlan builds the pod START plan. Mirrors the former StartCmd.Run's quadlet/direct
// branch: quadlet mode threads the systemd unit name; direct mode threads the fully-built
// `podman run -d` argv (buildStartArgs).
func resolvePodStartPlan(ctx context.Context, ex *sdk.Executor, box, instance string, opts spec.PodStartOpts) (*spec.PodLifecyclePlan, error) {
	rt, err := kit.ResolveRuntime()
	if err != nil {
		return nil, err
	}
	if rt.RunMode == "quadlet" {
		return resolvePodStartQuadlet(ctx, ex, box, instance, rt)
	}
	return resolvePodStartDirect(ctx, ex, box, instance, rt, opts)
}

// resolvePodStartQuadlet resolves the quadlet-mode start plan (the deployed/bed path): the plugin
// runs `systemctl --user start <svc>` (or `podman start <ctr>` for a direct-deploy marker) + mounts
// encrypted volumes.
func resolvePodStartQuadlet(ctx context.Context, ex *sdk.Executor, box, instance string, rt *kit.ResolvedRuntime) (*spec.PodLifecyclePlan, error) {
	exists, err := kit.QuadletExistsInstance(box, instance)
	if err != nil {
		return nil, err
	}
	directDeploy := !exists && IsDirectDeploy(box, instance)
	if !exists && !directDeploy {
		return nil, fmt.Errorf("not configured; run 'charly config %s' first", box)
	}
	engine, err := boxEngineForDeploy(ctx, ex, box, instance, rt.RunEngine)
	if err != nil {
		return nil, err
	}
	dc, err := loadDeploy(ctx, ex, "charly start")
	if err != nil {
		return nil, err
	}
	plan := &spec.PodLifecyclePlan{
		Mode:          "quadlet",
		SvcName:       kit.ServiceNameInstance(box, instance),
		ContainerName: kit.ContainerNameInstance(box, instance),
		DirectDeploy:  directDeploy,
		EngineBin:     kit.EngineBinary(engine),
	}
	// Encrypted-volume mounts are skipped in direct-deploy mode (those require systemd-run --scope).
	if !directDeploy {
		encJSON, err := resolvePodEncEnsurePlan(ctx, ex, dc, box, instance)
		if err != nil {
			return nil, err
		}
		plan.Enc = encJSON
	}
	plan.Tunnel = resolvePodTunnelPlan(dc, box, instance)
	return plan, nil
}

// boxEngineForDeploy wraps the "pod-config-box-engine" seam (ResolveBoxEngineForDeploy — reads the
// per-host deploy config's Engine override, loader-coupled).
func boxEngineForDeploy(ctx context.Context, ex *sdk.Executor, box, instance, globalEngine string) (string, error) {
	var rep spec.PodConfigBoxEngineReply
	if err := hostBuild(ctx, ex, podConfigBoxEngineKind, spec.PodConfigBoxEngineRequest{Box: box, Instance: instance, GlobalEngine: globalEngine}, &rep); err != nil {
		return globalEngine, err
	}
	return rep.Engine, nil
}

// podRuntimeImage is the resolved pod image context shared by the start-plan resolver and the F12
// shell resolver (R3): the runtime-detected devices, the resolved engine + image ref + baked
// metadata, the per-host deploy config, and the resolved volume backing.
type podRuntimeImage struct {
	detected   spec.DetectedDevices
	engine     string
	imageRef   string
	meta       *spec.BoxMetadata
	dc         *deploykit.BundleConfig
	volumes    []deploykit.VolumeMount
	bindMounts []deploykit.ResolvedBindMount
}

// resolvePodRuntimeImage resolves the pod's runtime image context — the identical HEAD both
// resolvePodStartDirect (`charly start` direct-mode) and resolvePodShellPlan (`charly shell`)
// compute: device detection + CDI, the deploy overlay, the image ref, and the volume backing.
func resolvePodRuntimeImage(ctx context.Context, ex *sdk.Executor, box, instance, tag string, rt *kit.ResolvedRuntime, noAutoDetect bool, volumeFlag, bind []string) (*podRuntimeImage, error) {
	var detRep spec.PodConfigDetectDevicesReply
	if err := hostBuild(ctx, ex, podConfigDetectDevicesKind, spec.PodConfigDetectDevicesRequest{NoAutoDetect: noAutoDetect, Engine: rt.RunEngine}, &detRep); err != nil {
		return nil, err
	}
	var detected spec.DetectedDevices
	if len(detRep.DetectedJSON) > 0 {
		if err := json.Unmarshal(detRep.DetectedJSON, &detected); err != nil {
			return nil, err
		}
	}
	engine := rt.RunEngine

	dc, err := loadDeploy(ctx, ex, "charly pod runtime image")
	if err != nil {
		return nil, err
	}
	var deployVolumes []spec.DeployVolume
	if dc != nil {
		if overlay, ok := dc.Lookup(box, instance); ok {
			deployVolumes = overlay.Volume
		}
	}

	var refRep spec.PodConfigResolveRefReply
	if err := hostBuild(ctx, ex, podConfigResolveRefKind, spec.PodConfigResolveRefRequest{Box: box, Instance: instance, Tag: tag}, &refRep); err != nil {
		return nil, err
	}
	imageRef := refRep.ImageRef
	var ensureRep spec.PodConfigEnsureImageReply
	if err := hostBuild(ctx, ex, podConfigEnsureImageKind, spec.PodConfigEnsureImageRequest{ImageRef: imageRef, BuildEngine: rt.BuildEngine}, &ensureRep); err != nil {
		return nil, err
	}
	var meta spec.BoxMetadata
	if err := json.Unmarshal(ensureRep.MetaJSON, &meta); err != nil {
		return nil, err
	}
	if meta.Engine != "" {
		engine = meta.Engine
	}
	if dc != nil {
		deploykit.MergeDeployOntoMetadata(&meta, dc, box, instance)
	}

	cliVolumes := parseVolumeFlagsCLI(volumeFlag, bind)
	volumes, bindMounts := deploykit.ResolveVolumeBacking(box, instance, meta.Volume, mergeVolumeConfigsLocal(deployVolumes, cliVolumes), meta.Home, rt.EncryptedStoragePath, rt.VolumesPath)
	if meta.Registry != "" {
		var refRep2 spec.PodConfigResolveRefReply
		if err := hostBuild(ctx, ex, podConfigResolveRefKind, spec.PodConfigResolveRefRequest{Box: box, Instance: instance, Tag: tag}, &refRep2); err == nil {
			imageRef = refRep2.ImageRef
		}
	}
	return &podRuntimeImage{detected: detected, engine: engine, imageRef: imageRef, meta: &meta, dc: dc, volumes: volumes, bindMounts: bindMounts}, nil
}

// resolvePodStartDirect resolves the direct-mode (non-quadlet) start plan: the full `podman run -d`
// argv the plugin execs. It returns RunArgv = buildStartArgs(…) instead of running it, and threads
// the enc-ensure + tunnel legs. The sidecar-in-direct-mode rejection is preserved (sidecars require
// quadlet).
func resolvePodStartDirect(ctx context.Context, ex *sdk.Executor, box, instance string, rt *kit.ResolvedRuntime, opts spec.PodStartOpts) (*spec.PodLifecyclePlan, error) {
	img, err := resolvePodRuntimeImage(ctx, ex, box, instance, "", rt, opts.NoAutoDetect, opts.VolumeFlag, opts.Bind)
	if err != nil {
		return nil, err
	}
	detected, engine, imageRef, meta, dc := img.detected, img.engine, img.imageRef, img.meta, img.dc
	volumes, bindMounts := img.volumes, img.bindMounts

	if dc != nil {
		if overlay, ok := dc.Lookup(box, instance); ok && len(overlay.Sidecar) > 0 {
			return nil, fmt.Errorf("image %s has sidecars configured in charly.yml; use 'charly config %s && charly start %s' (sidecars require quadlet mode)", box, box, box)
		}
	}

	uid, gid, home := meta.UID, meta.GID, meta.Home
	ports := meta.Port
	security := meta.Security
	network := meta.Network
	entrypoint := resolveEntrypointFromMeta(meta)

	encJSON, err := resolvePodEncEnsurePlan(ctx, ex, dc, box, instance)
	if err != nil {
		return nil, err
	}
	if err := deploykit.VerifyBindMounts(bindMounts, box); err != nil {
		return nil, err
	}

	deployEnv := meta.Env
	startCtrName := kit.ContainerNameInstance(box, instance)
	startAccepted := deploykit.AcceptedEnvSet(meta.EnvAccept, meta.EnvRequire)
	var startGlobalEnv []string
	if dc != nil {
		startGlobalEnv = dc.GlobalEnvForImage(deploykit.DeployKey(box, instance), startCtrName, startAccepted)
	}
	envVars, err := kit.ResolveEnvVars(startGlobalEnv, deployEnv, "", workspaceBindHost(bindMounts), opts.EnvFile, opts.Env)
	if err != nil {
		return nil, err
	}

	if !security.Privileged {
		security.Devices = deploykit.AppendUnique(security.Devices, detected.Devices...)
		if detected.AMDGPU {
			security.GroupAdd = appendGroupsForAMDGPU(security.GroupAdd)
		}
	}
	envVars = appendAutoDetectedEnv(envVars, detected)

	resolvedNetwork, netErr := kit.ResolveNetwork(network, engine)
	if netErr != nil {
		return nil, netErr
	}

	if len(opts.Port) > 0 {
		ports, err = kit.ApplyPortOverrides(ports, opts.Port)
		if err != nil {
			return nil, err
		}
		inputJSON, _ := json.Marshal(deploykit.SaveDeployStateInput{Ports: ports, SetPorts: true})
		_ = hostBuild(ctx, ex, deployConfigSaveStateKind, spec.DeployConfigSaveStateRequest{Box: box, Instance: instance, InputJSON: inputJSON}, nil)
	}
	if conflicts := kit.CheckPortAvailability(ports, rt.BindAddress, engine); len(conflicts) > 0 {
		return nil, fmt.Errorf("port conflicts detected:%s", kit.FormatPortConflicts(conflicts, box))
	}

	var deployBox *spec.BundleNode
	if dc != nil {
		if overlay, ok := dc.Lookup(box, instance); ok {
			deployBox = &overlay
		}
	}
	agentFwd := kit.ResolveAgentForwarding(rt, deployBox, home)
	for _, v := range agentFwd.Volumes {
		security.Mounts = deploykit.AppendUnique(security.Mounts, v)
	}
	envVars = append(envVars, agentFwd.Env...)

	name := kit.ContainerNameInstance(box, instance)
	workDir := deploykit.ResolveWorkingDir(volumes, bindMounts, home, box, instance)
	argv := buildStartArgs(engine, imageRef, uid, gid, ports, name, volumes, bindMounts, detected.GPU, rt.BindAddress, envVars, security, entrypoint, workDir, resolvedNetwork)

	tunnel := resolvePodTunnelPlan(dc, box, instance)

	return &spec.PodLifecyclePlan{
		Mode: "direct", ContainerName: name, RunArgv: argv, EngineBin: kit.EngineBinary(engine),
		Enc: encJSON, Tunnel: tunnel,
	}, nil
}

// resolvePodStopPlan builds the pod STOP plan.
func resolvePodStopPlan(ctx context.Context, ex *sdk.Executor, box, instance string, unmount bool) (*spec.PodLifecyclePlan, error) {
	rt, err := kit.ResolveRuntime()
	if err != nil {
		return nil, err
	}
	quadletActive, _ := kit.QuadletExistsInstance(box, instance)
	engine, err := boxEngineForDeploy(ctx, ex, box, instance, rt.RunEngine)
	if err != nil {
		return nil, err
	}
	dc, err := loadDeploy(ctx, ex, "charly stop")
	if err != nil {
		return nil, err
	}
	plan := &spec.PodLifecyclePlan{
		ContainerName: kit.ContainerNameInstance(box, instance),
		SvcName:       kit.ServiceNameInstance(box, instance),
		EngineBin:     kit.EngineBinary(engine),
		Unmount:       unmount,
		Tunnel:        resolvePodTunnelPlan(dc, box, instance),
	}
	if quadletActive {
		plan.Mode = "quadlet"
	} else {
		plan.Mode = "direct"
	}
	if unmount {
		encJSON, err := resolvePodEncUnmountPlan(dc, box, instance)
		if err != nil {
			return nil, err
		}
		plan.Enc = encJSON
	}
	return plan, nil
}
