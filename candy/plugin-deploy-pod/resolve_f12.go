package deploypod

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// resolve_f12.go — the F12 live-stdio resolvers (`charly shell` / `charly cmd` / `charly logs`)
// moved from charly-core's pod_lifecycle_resolve.go VERBATIM. Interactive/WrapPTY are HOST-RESOLVED
// booleans (spec.PodShellOpts) — this file NEVER re-derives isTerminal() against its own stdio
// (the plugin's os.Stdout is not the operator's terminal); it only reads the opts.

// resolvePodAttachPlan dispatches the F12 Attach op to the shell resolver (tty=true) or the cmd
// resolver (tty=false).
func resolvePodAttachPlan(ctx context.Context, ex *sdk.Executor, box, instance string, opts spec.PodAttachOpts) (*spec.PodLiveStdioPlan, error) {
	if opts.Tty {
		return resolvePodShellPlan(ctx, ex, box, instance, opts.Cmd, opts.Shell)
	}
	return resolvePodCmdPlan(ctx, ex, box, instance, opts.Cmd, opts.CmdOpts)
}

// resolvePodShellPlan resolves `charly shell`: exec into the running container (`podman exec -it`)
// when it is up, else an ephemeral `podman run --rm -it` of the image.
func resolvePodShellPlan(ctx context.Context, ex *sdk.Executor, box, instance string, cmd []string, opts spec.PodShellOpts) (*spec.PodLiveStdioPlan, error) {
	command := strings.Join(cmd, " ")

	rt, err := kit.ResolveRuntime()
	if err != nil {
		return nil, err
	}
	img, err := resolvePodRuntimeImage(ctx, ex, box, instance, opts.Tag, rt, opts.NoAutoDetect, opts.VolumeFlag, opts.Bind)
	if err != nil {
		return nil, err
	}
	detected, engine, imageRef, meta, dc := img.detected, img.engine, img.imageRef, img.meta, img.dc
	volumes, bindMounts := img.volumes, img.bindMounts

	uid, gid, home := meta.UID, meta.GID, meta.Home
	ports, security, network := meta.Port, meta.Security, meta.Network

	shellCtrName := kit.ContainerNameInstance(box, instance)
	shellAccepted := deploykit.AcceptedEnvSet(meta.EnvAccept, meta.EnvRequire)
	var shellGlobalEnv []string
	if dc != nil {
		shellGlobalEnv = dc.GlobalEnvForImage(deploykit.DeployKey(box, instance), shellCtrName, shellAccepted)
	}
	envVars, err := kit.ResolveEnvVars(shellGlobalEnv, meta.Env, "", workspaceBindHost(bindMounts), opts.EnvFile, opts.Env)
	if err != nil {
		return nil, err
	}

	var deployBox *spec.BundleNode
	if dc != nil {
		if overlay, ok := dc.Lookup(box, instance); ok {
			deployBox = &overlay
		}
	}
	agentFwd := kit.ResolveAgentForwarding(rt, deployBox, home)

	name := kit.ContainerNameInstance(box, instance)
	if containerRunningLocal(engine, name) {
		execEnv := append(append([]string{}, envVars...), agentFwd.Env...)
		workDir := deploykit.ResolveWorkingDir(volumes, bindMounts, home, box, instance)
		argv := buildExecArgs(engine, name, uid, gid, command, execEnv, workDir, opts.Interactive)
		return &spec.PodLiveStdioPlan{Script: hostAttachScript(argv, opts.WrapPTY)}, nil
	}

	if err := deploykit.VerifyBindMounts(bindMounts, box); err != nil {
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
	resolvedNetwork, err := kit.ResolveNetwork(network, engine)
	if err != nil {
		return nil, err
	}
	workDir := deploykit.ResolveWorkingDir(volumes, bindMounts, home, box, instance)
	argv := buildShellArgs(engine, imageRef, uid, gid, ports, volumes, bindMounts, detected.GPU, command, rt.BindAddress, envVars, security, workDir, opts.Interactive, resolvedNetwork)
	return &spec.PodLiveStdioPlan{Script: hostAttachScript(argv, opts.WrapPTY)}, nil
}

// resolvePodCmdPlan resolves `charly cmd`: an `<engine> exec -i [-e agentEnv] <ctr> sh -c <command>`
// into the RUNNING container (or a named sidecar), with agent-forwarding env resolved.
func resolvePodCmdPlan(ctx context.Context, ex *sdk.Executor, box, instance string, cmd []string, opts spec.PodCmdOpts) (*spec.PodLiveStdioPlan, error) {
	var engine, name string
	var err error
	if opts.Sidecar != "" {
		engine, name, err = resolveSidecarContainerLocal(ctx, ex, box, instance, opts.Sidecar)
	} else {
		engine, name, err = resolveContainerLocal(ctx, ex, box, instance)
	}
	if err != nil {
		return nil, err
	}

	var agentEnv []string
	if rt, rtErr := kit.ResolveRuntime(); rtErr == nil {
		var deployBox *spec.BundleNode
		if dc, lerr := loadDeploy(ctx, ex, "charly cmd"); lerr == nil && dc != nil {
			if overlay, ok := dc.Lookup(box, instance); ok {
				deployBox = &overlay
			}
		}
		agentFwd := kit.ResolveAgentForwarding(rt, deployBox, "")
		agentEnv = agentFwd.Env
	}

	argv := []string{engine, "exec", "-i"}
	for _, e := range agentEnv {
		argv = append(argv, "-e", e)
	}
	argv = append(argv, name, "sh", "-c", strings.Join(cmd, " "))
	return &spec.PodLiveStdioPlan{Script: shellQuoteArgs(argv)}, nil
}

// resolvePodLogsPlan resolves `charly logs [-f]`: quadlet mode → `journalctl --user -u <svc> [-f]
// [-n N]`; container mode → `<engine> logs [-f] [--tail N] <ctr>` (or the named sidecar).
func resolvePodLogsPlan(ctx context.Context, ex *sdk.Executor, box, instance string, opts spec.PodLogsOpts) (*spec.PodLiveStdioPlan, error) {
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

	engineName, err := boxEngineForDeploy(ctx, ex, boxName, instance, rt.RunEngine)
	if err != nil {
		return nil, err
	}
	engine := kit.EngineBinary(engineName)
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

// hostAttachScript renders a resolved exec/run argv into the single script string the pod plugin
// runs as `bash -c "<script>"` over exec.RunInteractive. wrapPTY (HOST-resolved: forced --tty
// without a real terminal — an automation tool) wraps the command in `script(1)` for a PTY.
func hostAttachScript(argv []string, wrapPTY bool) string {
	if wrapPTY {
		inner := shellQuoteArgs(argv)
		return shellQuoteArgs([]string{"script", "-qefc", inner, "/dev/null"})
	}
	return shellQuoteArgs(argv)
}

// buildShellArgs constructs the `<engine> run --rm -it|-i …` argument list. interactive is the
// HOST-resolved tty boolean (replaces the former package-level forceTTY||isTerminal() read).
func buildShellArgs(engine, imageRef string, uid, gid int, ports []string, volumes []deploykit.VolumeMount, bindMounts []deploykit.ResolvedBindMount, gpu bool, command string, bindAddr string, envVars []string, security spec.SecurityConfig, workingDir string, interactive bool, network ...string) []string {
	binary := kit.EngineBinary(engine)
	interactiveFlag := "-i"
	if interactive {
		interactiveFlag = "-it"
	}
	args := []string{
		binary, "run", "--rm", interactiveFlag,
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

// buildExecArgs constructs the `<engine> exec -it|-i …` list for attaching to a running container.
func buildExecArgs(engine, name string, uid, gid int, command string, envVars []string, workingDir string, interactive bool) []string {
	binary := kit.EngineBinary(engine)
	interactiveFlag := "-i"
	if interactive {
		interactiveFlag = "-it"
	}
	args := []string{
		binary, "exec", interactiveFlag,
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

// shellQuoteArgs joins args into a shell-safe command string.
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

// containerRunningLocal mirrors charly-core shell.go's defaultContainerRunning (the `<engine>
// container inspect --format '{{.State.Running}}'`-based check) VERBATIM — pure engine-exec probe.
func containerRunningLocal(engine, name string) bool {
	out, err := exec.Command(kit.EngineBinary(engine), "container", "inspect", "--format", "{{.State.Running}}", name).Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "true"
}

// resolveContainerLocal mirrors charly-core container.go's resolveContainer.
func resolveContainerLocal(ctx context.Context, ex *sdk.Executor, box, instance string) (engine, name string, err error) {
	if box == "." {
		return "", "", nil
	}
	rt, err := kit.ResolveRuntime()
	if err != nil {
		return "", "", err
	}
	boxName := resolveBoxName(box)
	runEngine, err := boxEngineForDeploy(ctx, ex, boxName, instance, rt.RunEngine)
	if err != nil {
		return "", "", err
	}
	engine = kit.EngineBinary(runEngine)
	name = kit.ContainerNameInstance(boxName, instance)
	if !containerRunningLocal(engine, name) {
		return "", "", fmt.Errorf("container %s is not running", name)
	}
	return engine, name, nil
}

// resolveSidecarContainerLocal mirrors charly-core volume_cp_tags_cmd.go's resolveSidecarContainer.
func resolveSidecarContainerLocal(ctx context.Context, ex *sdk.Executor, box, instance, sidecar string) (engine, name string, err error) {
	rt, err := kit.ResolveRuntime()
	if err != nil {
		return "", "", err
	}
	boxName := resolveBoxName(box)
	runEngine, err := boxEngineForDeploy(ctx, ex, boxName, instance, rt.RunEngine)
	if err != nil {
		return "", "", err
	}
	engine = kit.EngineBinary(runEngine)
	name = kit.SidecarContainerNameInstance(boxName, instance, sidecar)
	if !containerRunningLocal(engine, name) {
		return "", "", fmt.Errorf("sidecar container %s is not running", name)
	}
	return engine, name, nil
}
