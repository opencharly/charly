package main

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// pod_lifecycle_resolve.go — the HOST-side RESOLUTION half of the K4 pod-lifecycle deep-body move.
// The pod start/stop bodies (podman run / systemctl / journalctl) EXECUTE in candy/plugin-deploy-pod
// over the F6 OpStart/OpStop/OpShell channel (killing the former podCli("start"/…) `charly`-reentries);
// the RESOLUTION — image/metadata/overlay/volumes/env/security/ports/network/agent-forwarding +
// buildStartArgs + the enc/tunnel inputs the plugin composes — is registered TRACKED P13-KERNEL EXIT
// migration inventory (#59): it needs core-only types (BoxMetadata/SecurityConfig/ExtractMetadata)
// and fills a spec.PodLifecyclePlan the host threads to the plugin. It moves through the ONE
// venue-scoped-executor-session seam the P13-KERNEL wave builds, alongside bundle's deploy-add/
// deploy-del resolver kernel (R3 across waves, never two seams) — never a permanent core residence.
// This file is that resolution, relocated VERBATIM from the former core StartCmd.runDirect/
// runQuadlet + StopCmd (now command:start/command:stop in candy/plugin-pod, reaching this file via
// HostBuild("pod-start")/HostBuild("pod-stop") as podStartCmd/podStopCmd in start.go — parity by
// construction, the SAME resolver helpers, same order). The ARBITER claim is NOT resolved here — it
// is a shared host-process lease the F6 dispatch BRACKETS the plugin op with (acquire before
// OpStart, release after OpStop + on the failure path); see pod_lifecycle_bracket.go.

// resolvePodStartPlan builds the pod START plan the plugin executes. It mirrors StartCmd.Run's
// quadlet/direct branch: quadlet mode threads the systemd unit name (the plugin runs `systemctl
// --user start <svc>`); direct mode threads the fully-built `podman run -d` argv (buildStartArgs).
// The enc + tunnel legs are resolved host-side into their verb inputs (empty ⇒ the plugin skips
// that leg — the common plain-pod case). opts carries the direct-mode CLI extras (--env/--port/
// --volume/--bind + auto-detect) `charly start` accepts; the quadlet path ignores them.
func resolvePodStartPlan(box, instance string, opts podStartOpts) (*spec.PodLifecyclePlan, error) {
	rt, err := kit.ResolveRuntime()
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
func resolvePodStartQuadlet(box, instance string, rt *kit.ResolvedRuntime) (*spec.PodLifecyclePlan, error) {
	exists, err := kit.QuadletExistsInstance(box, instance)
	if err != nil {
		return nil, err
	}
	directDeploy := !exists && IsDirectDeploy(box, instance)
	if !exists && !directDeploy {
		return nil, fmt.Errorf("not configured; run 'charly config %s' first", box)
	}
	plan := &spec.PodLifecyclePlan{
		Mode:          "quadlet",
		SvcName:       kit.ServiceNameInstance(box, instance),
		ContainerName: kit.ContainerNameInstance(box, instance),
		DirectDeploy:  directDeploy,
		EngineBin:     kit.EngineBinary(ResolveBoxEngineForDeploy(box, instance, rt.RunEngine)),
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
	dc := deploykit.LoadDeployConfigForRead("charly start tunnel")
	ctrName := kit.ContainerNameInstance(box, instance)
	imageRef := containerImage("podman", ctrName)
	if imageRef == "" {
		return nil
	}
	meta, err := ExtractMetadata("podman", imageRef)
	if err != nil || meta == nil {
		return nil
	}
	deploykit.MergeDeployOntoMetadata(meta, dc, box, instance)
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
func resolvePodStartDirect(box, instance string, rt *kit.ResolvedRuntime, opts podStartOpts) (*spec.PodLifecyclePlan, error) {
	img, err := resolvePodRuntimeImage(box, instance, "", rt, opts.NoAutoDetect, opts.VolumeFlag, opts.Bind)
	if err != nil {
		return nil, err
	}
	detected, engine, imageRef, meta, dc := img.detected, img.engine, img.imageRef, img.meta, img.dc
	volumes, bindMounts := img.volumes, img.bindMounts

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

	envAccepts := meta.EnvAccept
	envRequires := meta.EnvRequire

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
	startCtrName := kit.ContainerNameInstance(box, instance)
	startAccepted := AcceptedEnvSet(envAccepts, envRequires)
	startGlobalEnv := dc.GlobalEnvForImage(deploykit.DeployKey(box, instance), startCtrName, startAccepted)
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

	resolvedNetwork, netErr := ResolveNetwork(network, engine)
	if netErr != nil {
		return nil, netErr
	}

	if len(opts.Port) > 0 {
		ports, err = ApplyPortOverrides(ports, opts.Port)
		if err != nil {
			return nil, err
		}
		deploykit.SaveDeployState(box, instance, deploykit.SaveDeployStateInput{Ports: ports, SetPorts: true}, marshalDeployNode)
	}
	if conflicts := CheckPortAvailability(ports, rt.BindAddress, engine); len(conflicts) > 0 {
		return nil, fmt.Errorf("port conflicts detected:%s", FormatPortConflicts(conflicts, box))
	}

	var deployBox *spec.BundleNode
	if overlay, ok := dc.Lookup(box, instance); ok {
		deployBox = &overlay
	}
	agentFwd := ResolveAgentForwarding(rt, deployBox, home)
	for _, v := range agentFwd.Volumes {
		security.Mounts = deploykit.AppendUnique(security.Mounts, v)
	}
	envVars = append(envVars, agentFwd.Env...)

	name := kit.ContainerNameInstance(box, instance)
	workDir := deploykit.ResolveWorkingDir(volumes, bindMounts, home, box, instance)
	argv := buildStartArgs(engine, imageRef, uid, gid, ports, name, volumes, bindMounts, detected.GPU, rt.BindAddress, envVars, security, entrypoint, workDir, resolvedNetwork)

	return &spec.PodLifecyclePlan{
		Mode:          "direct",
		ContainerName: name,
		RunArgv:       argv,
		EngineBin:     kit.EngineBinary(engine),
		Enc:           enc,
		Tunnel:        resolvePodTunnel(box, instance),
	}, nil
}

// resolvePodStopPlan builds the pod STOP plan the plugin executes: the systemctl/engine stop of the
// resolved unit/container + (optionally) the tunnel-stop + enc-unmount legs the plugin composes.
// Mirrors StopCmd.Run (minus the arbiter release, which the F6 dispatch brackets host-side).
func resolvePodStopPlan(box, instance string, unmount bool) (*spec.PodLifecyclePlan, error) {
	rt, err := kit.ResolveRuntime()
	if err != nil {
		return nil, err
	}
	quadletActive, _ := kit.QuadletExistsInstance(box, instance)
	plan := &spec.PodLifecyclePlan{
		ContainerName: kit.ContainerNameInstance(box, instance),
		SvcName:       kit.ServiceNameInstance(box, instance),
		EngineBin:     kit.EngineBinary(ResolveBoxEngineForDeploy(box, instance, rt.RunEngine)),
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
	plan, err := encPlanFor(box, instance, "", deploykit.DeployStorageDir(box, instance))
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

// ---------------------------------------------------------------------------
// F12 live-stdio resolvers — the HOST-side RESOLUTION of `charly shell` / `charly cmd` / `charly logs`
// into a single #PodLiveStdioPlan{script} the plugin runs over the served venue executor via
// RunInteractive/RunStream. Relocated VERBATIM from the former inline shell.go/cmd.go/commands.go
// bodies (their syscall.Exec/os.exec attach became the executor M-leg; only the RESOLUTION lands here,
// #59 inventory). resolvePodShellPlan R3-shares the image-context resolution with resolvePodStartDirect
// via resolvePodRuntimeImage.
// ---------------------------------------------------------------------------

// podRuntimeImage is the resolved pod image context shared by the start-plan resolver and the F12
// shell resolver (R3): the runtime-detected devices, the resolved engine + image ref + baked metadata,
// the per-host deploy config, and the resolved volume backing.
type podRuntimeImage struct {
	detected   DetectedDevices
	engine     string
	imageRef   string
	meta       *BoxMetadata
	dc         *deploykit.BundleConfig
	volumes    []deploykit.VolumeMount
	bindMounts []deploykit.ResolvedBindMount
}

// resolvePodRuntimeImage resolves the pod's runtime image context — the identical HEAD both
// resolvePodStartDirect (`charly start` direct-mode) and resolvePodShellPlan (`charly shell`) compute:
// device detection + CDI, the deploy overlay, the image ref (EnsureImage + metadata + engine-from-meta
// + deploy overlay merge), and the volume backing. The divergent tails (start: enc/ports/tunnel/
// buildStartArgs; shell: the running-exec vs ephemeral-run branch) stay in each caller.
func resolvePodRuntimeImage(box, instance, tag string, rt *kit.ResolvedRuntime, noAutoDetect bool, volumeFlag, bind []string) (*podRuntimeImage, error) {
	var detected DetectedDevices
	if !noAutoDetect {
		detected = DetectHostDevices()
		LogDetectedDevices(detected)
	}
	engine := rt.RunEngine
	if detected.GPU && engine == "podman" {
		EnsureCDI()
	}

	dc := deploykit.LoadDeployConfigForRead("charly pod runtime image")
	var deployVolumes []DeployVolumeConfig
	if overlay, ok := dc.Lookup(box, instance); ok {
		deployVolumes = overlay.Volume
	}

	deployBoxName := resolveDeployBoxName(box, instance)
	imageRef := resolveShellImageRef("", deployBoxName, tag)
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
	deploykit.MergeDeployOntoMetadata(meta, dc, box, instance)

	cliVolumes := parseVolumeFlagsStandalone(volumeFlag, bind)
	volumes, bindMounts := deploykit.ResolveVolumeBacking(box, instance, meta.Volume, mergeVolumeConfigs(deployVolumes, cliVolumes), meta.Home, rt.EncryptedStoragePath, rt.VolumesPath)
	if meta.Registry != "" {
		imageRef = resolveShellImageRef(meta.Registry, deployBoxName, tag)
	}
	return &podRuntimeImage{detected: detected, engine: engine, imageRef: imageRef, meta: meta, dc: dc, volumes: volumes, bindMounts: bindMounts}, nil
}

// resolvePodAttachPlan dispatches the F12 Attach op to the shell resolver (tty=true, the `charly shell`
// interactive `-it`/ephemeral-run leg) or the cmd resolver (tty=false, the `charly cmd` `-i` exec).
func resolvePodAttachPlan(ctx context.Context, box, instance string, cmd []string, tty bool) (*spec.PodLiveStdioPlan, error) {
	if tty {
		return resolvePodShellPlan(box, instance, cmd, podShellOptsFromCtx(ctx))
	}
	return resolvePodCmdPlan(box, instance, cmd, podCmdOptsFromCtx(ctx))
}

// resolvePodShellPlan resolves `charly shell`: exec into the running container (`podman exec -it`) when
// it is up, else an ephemeral `podman run --rm -it` of the image. Relocated VERBATIM from the former
// ShellCmd.Run (same helpers, same order — parity by construction), stopping BEFORE the syscall.Exec:
// it returns the shell-quoted argv as the plan Script (with the `--tty` PTY-wrap when forced without a
// real terminal). command is the `-c` argv (empty ⇒ an interactive login shell).
func resolvePodShellPlan(box, instance string, cmd []string, opts podShellOpts) (*spec.PodLiveStdioPlan, error) {
	forceTTY = opts.ForceTTY // buildShellArgs/buildExecArgs + hostAttachScript read this global (relocated from ShellCmd.Run)
	command := strings.Join(cmd, " ")

	rt, err := kit.ResolveRuntime()
	if err != nil {
		return nil, err
	}
	img, err := resolvePodRuntimeImage(box, instance, opts.Tag, rt, opts.NoAutoDetect, opts.VolumeFlag, opts.Bind)
	if err != nil {
		return nil, err
	}
	detected, engine, imageRef, meta, dc := img.detected, img.engine, img.imageRef, img.meta, img.dc
	volumes, bindMounts := img.volumes, img.bindMounts

	uid, gid, home := meta.UID, meta.GID, meta.Home
	ports, security, network := meta.Port, meta.Security, meta.Network

	shellCtrName := kit.ContainerNameInstance(box, instance)
	shellAccepted := AcceptedEnvSet(meta.EnvAccept, meta.EnvRequire)
	shellGlobalEnv := dc.GlobalEnvForImage(deploykit.DeployKey(box, instance), shellCtrName, shellAccepted)
	envVars, err := kit.ResolveEnvVars(shellGlobalEnv, meta.Env, "", workspaceBindHost(bindMounts), opts.EnvFile, opts.Env)
	if err != nil {
		return nil, err
	}

	var deployBox *spec.BundleNode
	if overlay, ok := dc.Lookup(box, instance); ok {
		deployBox = &overlay
	}
	agentFwd := ResolveAgentForwarding(rt, deployBox, home)

	name := kit.ContainerNameInstance(box, instance)
	// Running container → exec into it (env-only; can't add volumes/devices to a running container).
	if containerRunning(engine, name) {
		execEnv := append(slices.Clone(envVars), agentFwd.Env...)
		workDir := deploykit.ResolveWorkingDir(volumes, bindMounts, home, box, instance)
		argv := buildExecArgs(engine, name, uid, gid, command, execEnv, workDir)
		return &spec.PodLiveStdioPlan{Script: hostAttachScript(argv)}, nil
	}

	// Ephemeral run of the image.
	if err := verifyBindMounts(bindMounts, box); err != nil {
		return nil, err
	}
	if !security.Privileged {
		security.Devices = deploykit.AppendUnique(security.Devices, detected.Devices...)
		if detected.AMDGPU {
			security.GroupAdd = appendGroupsForAMDGPU(security.GroupAdd)
		}
	}
	envVars = appendAutoDetectedEnv(envVars, detected)
	for _, v := range agentFwd.Volumes {
		security.Mounts = deploykit.AppendUnique(security.Mounts, v)
	}
	envVars = append(envVars, agentFwd.Env...)
	resolvedNetwork, err := ResolveNetwork(network, engine)
	if err != nil {
		return nil, err
	}
	workDir := deploykit.ResolveWorkingDir(volumes, bindMounts, home, box, instance)
	argv := buildShellArgs(engine, imageRef, uid, gid, ports, volumes, bindMounts, detected.GPU, command, rt.BindAddress, envVars, security, workDir, resolvedNetwork)
	return &spec.PodLiveStdioPlan{Script: hostAttachScript(argv)}, nil
}

// resolvePodCmdPlan resolves `charly cmd`: an `<engine> exec -i [-e agentEnv] <ctr> sh -c <command>`
// into the RUNNING container (or a named sidecar), with agent-forwarding env resolved host-side.
// Relocated from the former CmdCmd.Run (the `--notify` desktop-notification wrapper stays host-side in
// CmdCmd.Run). `-i` inherits the operator's stdin so `echo x | charly cmd <box> cat` reaches the venue.
func resolvePodCmdPlan(box, instance string, cmd []string, opts podCmdOpts) (*spec.PodLiveStdioPlan, error) {
	var engine, name string
	var err error
	if opts.Sidecar != "" {
		engine, name, err = resolveSidecarContainer(box, instance, opts.Sidecar)
	} else {
		engine, name, err = resolveContainer(box, instance)
	}
	if err != nil {
		return nil, err
	}

	// Agent-forwarding env vars for exec (host user's home as the GPG socket default; the sockets are
	// already mounted — this only affects env). Best-effort, exactly as CmdCmd.Run did.
	var agentEnv []string
	if rt, rtErr := kit.ResolveRuntime(); rtErr == nil {
		var deployBox *spec.BundleNode
		if overlay, ok := deploykit.LoadDeployConfigForRead("charly cmd").Lookup(box, instance); ok {
			deployBox = &overlay
		}
		hostHome, _ := os.UserHomeDir()
		agentFwd := ResolveAgentForwarding(rt, deployBox, hostHome)
		agentEnv = agentFwd.Env
	}

	argv := []string{engine, "exec", "-i"}
	for _, e := range agentEnv {
		argv = append(argv, "-e", e)
	}
	argv = append(argv, name, "sh", "-c", strings.Join(cmd, " "))
	return &spec.PodLiveStdioPlan{Script: shellQuoteArgs(argv)}, nil
}

// resolvePodLogsPlan resolves `charly logs [-f]`: quadlet mode → `journalctl --user -u <svc> [-f] [-n
// N]`; container mode → `<engine> logs [-f] [--tail N] <ctr>` (or the named sidecar). Relocated from
// the former LogsCmd.Run; the plugin streams it via exec.RunStream (killing the podCli("logs") reentry).
func resolvePodLogsPlan(box, instance string, opts LogsOpts) (*spec.PodLiveStdioPlan, error) {
	rt, err := kit.ResolveRuntime()
	if err != nil {
		return nil, err
	}
	boxName := resolveBoxName(box)

	if rt.RunMode == "quadlet" {
		svc := kit.ServiceNameInstance(boxName, instance)
		if opts.Sidecar != "" {
			svc = kit.SidecarContainerNameInstance(boxName, instance, opts.Sidecar) + ".service"
		}
		argv := []string{"journalctl", "--user", "-u", svc}
		if opts.Follow {
			argv = append(argv, "-f")
		}
		if opts.Tail > 0 {
			argv = append(argv, "-n", fmt.Sprintf("%d", opts.Tail))
		}
		return &spec.PodLiveStdioPlan{Script: shellQuoteArgs(argv)}, nil
	}

	engine := kit.EngineBinary(ResolveBoxEngineForDeploy(boxName, instance, rt.RunEngine))
	name := kit.ContainerNameInstance(boxName, instance)
	if opts.Sidecar != "" {
		name = kit.SidecarContainerNameInstance(boxName, instance, opts.Sidecar)
	}
	argv := []string{engine, "logs"}
	if opts.Follow {
		argv = append(argv, "-f")
	}
	if opts.Tail > 0 {
		argv = append(argv, "--tail", fmt.Sprintf("%d", opts.Tail))
	}
	argv = append(argv, name)
	return &spec.PodLiveStdioPlan{Script: shellQuoteArgs(argv)}, nil
}

// hostAttachScript renders a resolved exec/run argv into the single script string the pod plugin runs
// as `bash -c "<script>"` over exec.RunInteractive. When `--tty` is forced without a real terminal (an
// automation tool), it wraps the command in `script(1)` for a PTY — the relocated ShellCmd execCommand
// PTY-wrap. Otherwise the shell-quoted argv verbatim.
func hostAttachScript(argv []string) string {
	if forceTTY && !isTerminal() {
		inner := shellQuoteArgs(argv)
		return shellQuoteArgs([]string{"script", "-qefc", inner, "/dev/null"})
	}
	return shellQuoteArgs(argv)
}

// buildShellArgs constructs the `<engine> run --rm -it|-i …` argument list (relocated from shell.go).
func buildShellArgs(engine, imageRef string, uid, gid int, ports []string, volumes []deploykit.VolumeMount, bindMounts []deploykit.ResolvedBindMount, gpu bool, command string, bindAddr string, envVars []string, security SecurityConfig, workingDir string, network ...string) []string {
	binary := kit.EngineBinary(engine)
	interactive := "-i"
	if forceTTY || isTerminal() {
		interactive = "-it"
	}
	args := []string{
		binary, "run", "--rm", interactive,
		"-w", workingDir,
		"--user", fmt.Sprintf("%d:%d", uid, gid),
	}
	if len(network) > 0 && network[0] != "" {
		args = append(args, "--network", network[0])
	}
	if gpu {
		args = append(args, kit.GPURunArgs(engine)...)
	}
	args = append(args, deploykit.SecurityArgs(security)...)
	for _, port := range ports {
		args = append(args, "-p", deploykit.LocalizePort(port, bindAddr))
	}
	for _, vol := range volumes {
		args = append(args, "-v", fmt.Sprintf("%s:%s", vol.VolumeName, vol.ContainerPath))
	}
	for _, bm := range bindMounts {
		args = append(args, "-v", fmt.Sprintf("%s:%s", bm.HostPath, bm.ContPath))
	}
	for _, m := range security.Mounts {
		if after, ok := strings.CutPrefix(m, "tmpfs:"); ok {
			args = append(args, "--tmpfs", after)
		} else {
			args = append(args, "-v", m)
		}
	}
	if engine == "podman" && len(bindMounts) > 0 {
		args = append(args, fmt.Sprintf("--userns=keep-id:uid=%d,gid=%d", uid, gid))
	}
	for _, e := range envVars {
		args = append(args, "-e", e)
	}
	args = append(args, "--entrypoint", "bash", imageRef)
	if command != "" {
		args = append(args, "-c", command)
	}
	return args
}

// buildExecArgs constructs the `<engine> exec -it|-i …` list for attaching to a running container
// (relocated from shell.go).
func buildExecArgs(engine, name string, uid, gid int, command string, envVars []string, workingDir string) []string {
	binary := kit.EngineBinary(engine)
	interactive := "-i"
	if forceTTY || isTerminal() {
		interactive = "-it"
	}
	args := []string{
		binary, "exec", interactive,
		"--user", fmt.Sprintf("%d:%d", uid, gid),
		"-w", workingDir,
	}
	for _, e := range envVars {
		args = append(args, "-e", e)
	}
	args = append(args, name, "bash")
	if command != "" {
		args = append(args, "-c", command)
	}
	return args
}

// shellQuoteArgs joins args into a shell-safe command string (relocated from shell.go).
func shellQuoteArgs(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		if strings.ContainsAny(arg, " \t\n\"'\\$`!#&|;(){}[]<>?*~") {
			quoted[i] = "'" + strings.ReplaceAll(arg, "'", "'\"'\"'") + "'"
		} else {
			quoted[i] = arg
		}
	}
	return strings.Join(quoted, " ")
}
