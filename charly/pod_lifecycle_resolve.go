package main

import (
	"fmt"

	"github.com/opencharly/sdk/spec"
)

// pod_lifecycle_resolve.go — the HOST-side RESOLUTION half of the K4 pod-lifecycle deep-body move.
// The pod start/stop bodies (podman run / systemctl / journalctl) EXECUTE in candy/plugin-deploy-pod
// over the F6 OpStart/OpStop/OpShell channel (killing the former podCli("start"/…) `charly`-reentries);
// the RESOLUTION — image/metadata/overlay/volumes/env/security/ports/network/agent-forwarding +
// buildStartArgs + the enc/tunnel inputs the plugin composes — STAYS HOST-SIDE (config_image/deploy/
// network/enc/tunnel = #59 migration inventory) and fills a spec.PodLifecyclePlan the host threads to
// the plugin. This file is that resolution, relocated VERBATIM from StartCmd.runDirect/runQuadlet +
// StopCmd (parity by construction — the SAME resolver helpers, same order). The ARBITER claim is NOT
// resolved here — it is a shared host-process lease the F6 dispatch BRACKETS the plugin op with
// (acquire before OpStart, release after OpStop + on the failure path); see pod_lifecycle_bracket.go.

// resolvePodStartPlan builds the pod START plan the plugin executes. It mirrors StartCmd.Run's
// quadlet/direct branch: quadlet mode threads the systemd unit name (the plugin runs `systemctl
// --user start <svc>`); direct mode threads the fully-built `podman run -d` argv (buildStartArgs).
// The enc + tunnel legs are resolved host-side into their verb inputs (empty ⇒ the plugin skips
// that leg — the common plain-pod case). opts carries the direct-mode CLI extras (--env/--port/
// --volume/--bind + auto-detect) `charly start` accepts; the quadlet path ignores them.
func resolvePodStartPlan(box, instance string, opts podStartOpts) (*spec.PodLifecyclePlan, error) {
	rt, err := ResolveRuntime()
	if err != nil {
		return nil, err
	}
	if rt.RunMode == "quadlet" {
		return resolvePodStartQuadlet(box, instance, rt)
	}
	return resolvePodStartDirect(box, instance, rt, opts)
}

// podStartOpts carries the direct-mode `charly start` CLI extras through to the resolver (they apply
// only to the runDirect path; the quadlet path — the deployed/bed case — bakes config into the unit).
type podStartOpts struct {
	Env          []string
	EnvFile      string
	Port         []string
	VolumeFlag   []string
	Bind         []string
	NoAutoDetect bool
}

// resolvePodStartQuadlet resolves the quadlet-mode start plan (the deployed/bed path): the plugin
// runs `systemctl --user start <svc>` (or `podman start <ctr>` for a direct-deploy marker) + mounts
// encrypted volumes. Mirrors StartCmd.runQuadlet.
func resolvePodStartQuadlet(box, instance string, rt *ResolvedRuntime) (*spec.PodLifecyclePlan, error) {
	exists, err := quadletExistsInstance(box, instance)
	if err != nil {
		return nil, err
	}
	directDeploy := !exists && IsDirectDeploy(box, instance)
	if !exists && !directDeploy {
		return nil, fmt.Errorf("not configured; run 'charly config %s' first", box)
	}
	plan := &spec.PodLifecyclePlan{
		Mode:          "quadlet",
		SvcName:       serviceNameInstance(box, instance),
		ContainerName: containerNameInstance(box, instance),
		DirectDeploy:  directDeploy,
		EngineBin:     EngineBinary(ResolveBoxEngineForDeploy(box, instance, rt.RunEngine)),
	}
	// Encrypted-volume mounts are skipped in direct-deploy mode (those require
	// systemd-run --scope; matches runConfigDirect's warning path).
	if !directDeploy {
		enc, encErr := resolvePodEncEnsure(box, instance)
		if encErr != nil {
			return nil, encErr
		}
		plan.Enc = enc
	}
	plan.Tunnel = resolvePodTunnel(box, instance)
	return plan, nil
}

// resolvePodEncEnsure builds the pre-built spec.EncExecInput (ensure) the plugin InvokeProviders
// verb:enc with, or (nil, nil) when no encrypted volume is configured OR every one is already mounted
// (the keyring-resilient fast path preserved from ensureEncryptedMounts). resolveEncPassphrase +
// encPlanFor stay HOST-side (credential store + config reads the plugin cannot do); a passphrase
// resolution failure fails the start, exactly as the former in-core ensureEncryptedMounts did.
func resolvePodEncEnsure(box, instance string) (spec.RawBody, error) {
	plan, err := encPlanFor(box, instance, "", box)
	if err != nil || len(plan) == 0 {
		return nil, nil // no encrypted mounts configured (load error swallowed, as before)
	}
	anyNotReady := false
	for _, m := range plan {
		if !m.Initialized || !m.Mounted {
			anyNotReady = true
			break
		}
	}
	if !anyNotReady {
		return nil, nil // all mounted — skip the passphrase lookup + the enc leg
	}
	passphrase, err := resolveEncPassphrase(box, false)
	if err != nil {
		return nil, fmt.Errorf("resolving enc passphrase for %s: %w", box, err)
	}
	body, err := marshalJSON(spec.EncExecInput{
		Method:     spec.EncMethodEnsure,
		ImageID:    "charly-" + box,
		BoxName:    box,
		Passphrase: passphrase,
		Volumes:    plan,
	})
	return body, err
}

// resolvePodTunnel resolves the tunnel config (charly.yml-only; labels never carry tunnel) the plugin
// starts/stops, or nil when none is configured. Mirrors the StartCmd.runDirect tunnel branch.
func resolvePodTunnel(box, instance string) *spec.TunnelConfig {
	dc := loadDeployConfigForRead("charly start tunnel")
	ctrName := containerNameInstance(box, instance)
	imageRef := containerImage("podman", ctrName)
	if imageRef == "" {
		return nil
	}
	meta, err := ExtractMetadata("podman", imageRef)
	if err != nil || meta == nil {
		return nil
	}
	MergeDeployOntoMetadata(meta, dc, box, instance)
	if meta.Tunnel == nil {
		return nil
	}
	return TunnelConfigFromMetadata(meta)
}

// resolvePodStartDirect resolves the direct-mode (non-quadlet) start plan: the full `podman run -d`
// argv the plugin execs. Relocated VERBATIM from StartCmd.runDirect (the SAME resolver helpers, same
// order — parity by construction), stopping BEFORE the exec: it returns RunArgv = buildStartArgs(…)
// instead of running it, and threads the enc-ensure + tunnel legs the plugin composes (the mount +
// tunnel-start EFFECTS move to the plugin; the RESOLUTION stays here). The sidecar-in-direct-mode
// rejection is preserved (sidecars require quadlet).
func resolvePodStartDirect(box, instance string, rt *ResolvedRuntime, opts podStartOpts) (*spec.PodLifecyclePlan, error) {
	var detected DetectedDevices
	if !opts.NoAutoDetect {
		detected = DetectHostDevices()
		LogDetectedDevices(detected)
	}
	engine := rt.RunEngine
	if detected.GPU && engine == "podman" {
		EnsureCDI()
	}

	dc := loadDeployConfigForRead("charly start")
	var deployVolumes []DeployVolumeConfig
	if overlay, ok := dc.Lookup(box, instance); ok {
		deployVolumes = overlay.Volume
	}

	deployBoxName := resolveDeployBoxName(box, instance)
	imageRef := resolveShellImageRef("", deployBoxName, "")
	if err := EnsureImage(imageRef, rt); err != nil {
		return nil, err
	}
	meta, err := ExtractMetadata(engine, imageRef)
	if err != nil {
		return nil, err
	}
	if meta == nil {
		return nil, fmt.Errorf("image %s has no embedded metadata; rebuild with latest charly", imageRef)
	}
	engine = ResolveBoxEngineFromMeta(meta, rt.RunEngine)
	MergeDeployOntoMetadata(meta, dc, box, instance)

	if overlay, ok := dc.Lookup(box, instance); ok && len(overlay.Sidecar) > 0 {
		return nil, fmt.Errorf("image %s has sidecars configured in charly.yml; use 'charly config %s && charly start %s' (sidecars require quadlet mode)", box, box, box)
	}

	uid := meta.UID
	gid := meta.GID
	home := meta.Home
	ports := meta.Port
	security := meta.Security
	network := meta.Network
	entrypoint := resolveEntrypointFromMeta(meta)

	cliVolumes := parseVolumeFlagsStandalone(opts.VolumeFlag, opts.Bind)
	volumes, bindMounts := ResolveVolumeBacking(box, instance, meta.Volume, mergeVolumeConfigs(deployVolumes, cliVolumes), meta.Home, rt.EncryptedStoragePath, rt.VolumesPath)

	envAccepts := meta.EnvAccept
	envRequires := meta.EnvRequire
	if meta.Registry != "" {
		imageRef = resolveShellImageRef(meta.Registry, deployBoxName, "")
	}

	// enc-ensure resolves the mount input (the plugin does the mount); parity with
	// StartCmd.runDirect's ensureEncryptedMounts call.
	enc, encErr := resolvePodEncEnsure(box, instance)
	if encErr != nil {
		return nil, encErr
	}
	if err := verifyBindMounts(bindMounts, box); err != nil {
		return nil, err
	}

	deployEnv := meta.Env
	startCtrName := containerNameInstance(box, instance)
	startAccepted := AcceptedEnvSet(envAccepts, envRequires)
	startGlobalEnv := dc.GlobalEnvForImage(deployKey(box, instance), startCtrName, startAccepted)
	envVars, err := ResolveEnvVars(startGlobalEnv, deployEnv, "", workspaceBindHost(bindMounts), opts.EnvFile, opts.Env)
	if err != nil {
		return nil, err
	}

	if !security.Privileged {
		security.Devices = appendUnique(security.Devices, detected.Devices...)
		if detected.AMDGPU {
			security.GroupAdd = appendGroupsForAMDGPU(security.GroupAdd)
		}
	}
	envVars = appendAutoDetectedEnv(envVars, detected)

	resolvedNetwork, netErr := ResolveNetwork(network, engine)
	if netErr != nil {
		return nil, netErr
	}

	if len(opts.Port) > 0 {
		ports, err = ApplyPortOverrides(ports, opts.Port)
		if err != nil {
			return nil, err
		}
		saveDeployState(box, instance, SaveDeployStateInput{Ports: ports, SetPorts: true})
	}
	if conflicts := CheckPortAvailability(ports, rt.BindAddress, engine); len(conflicts) > 0 {
		return nil, fmt.Errorf("port conflicts detected:%s", FormatPortConflicts(conflicts, box))
	}

	var deployBox *BundleNode
	if overlay, ok := dc.Lookup(box, instance); ok {
		deployBox = &overlay
	}
	agentFwd := ResolveAgentForwarding(rt, deployBox, home)
	for _, v := range agentFwd.Volumes {
		security.Mounts = appendUnique(security.Mounts, v)
	}
	envVars = append(envVars, agentFwd.Env...)

	name := containerNameInstance(box, instance)
	workDir := resolveWorkingDir(volumes, bindMounts, home, box, instance)
	argv := buildStartArgs(engine, imageRef, uid, gid, ports, name, volumes, bindMounts, detected.GPU, rt.BindAddress, envVars, security, entrypoint, workDir, resolvedNetwork)

	return &spec.PodLifecyclePlan{
		Mode:          "direct",
		ContainerName: name,
		RunArgv:       argv,
		EngineBin:     EngineBinary(engine),
		Enc:           enc,
		Tunnel:        resolvePodTunnel(box, instance),
	}, nil
}

// resolvePodStopPlan builds the pod STOP plan the plugin executes: the systemctl/engine stop of the
// resolved unit/container + (optionally) the tunnel-stop + enc-unmount legs the plugin composes.
// Mirrors StopCmd.Run (minus the arbiter release, which the F6 dispatch brackets host-side).
func resolvePodStopPlan(box, instance string, unmount bool) (*spec.PodLifecyclePlan, error) {
	rt, err := ResolveRuntime()
	if err != nil {
		return nil, err
	}
	quadletActive, _ := quadletExistsInstance(box, instance)
	plan := &spec.PodLifecyclePlan{
		ContainerName: containerNameInstance(box, instance),
		SvcName:       serviceNameInstance(box, instance),
		EngineBin:     EngineBinary(ResolveBoxEngineForDeploy(box, instance, rt.RunEngine)),
		Unmount:       unmount,
		Tunnel:        resolvePodTunnel(box, instance),
	}
	if quadletActive {
		plan.Mode = "quadlet"
	} else {
		plan.Mode = "direct"
	}
	if unmount {
		enc, encErr := resolvePodEncUnmount(box, instance)
		if encErr != nil {
			return nil, encErr
		}
		plan.Enc = enc
	}
	return plan, nil
}

// resolvePodEncUnmount builds the spec.EncExecInput (unmount) the plugin InvokeProviders verb:enc
// with on `charly stop --unmount`, or nil when no encrypted volume is configured. Mirrors encUnmount.
func resolvePodEncUnmount(box, instance string) (spec.RawBody, error) {
	plan, err := encPlanFor(box, instance, "", deployStorageDir(box, instance))
	if err != nil || len(plan) == 0 {
		return nil, nil
	}
	body, err := marshalJSON(spec.EncExecInput{
		Method:  spec.EncMethodUnmount,
		ImageID: "charly-" + box,
		BoxName: box,
		Volumes: plan,
	})
	return body, err
}
