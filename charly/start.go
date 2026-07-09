package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// StartCmd launches a container with supervisord in the background
type StartCmd struct {
	Box             string   `arg:"" help:"Box name or remote ref (github.com/org/repo/box[@version])"`
	Tag             string   `long:"tag" help:"Image CalVer tag (empty = newest local CalVer resolved via the ai.opencharly.version OCI label)"`
	Build           bool     `long:"build" help:"Force local build instead of pulling from registry"`
	Env             []string `short:"e" long:"env" sep:"none" help:"Set container env var (direct mode only)"`
	EnvFile         string   `long:"env-file" help:"Load env vars from file (direct mode only)"`
	Instance        string   `short:"i" long:"instance" help:"Instance name for running multiple containers of the same box"`
	Port            []string `short:"p" help:"Remap host port (direct mode only)"`
	VolumeFlag      []string `long:"volume" short:"v" help:"Configure volume backing (name:type[:path])"`
	Bind            []string `long:"bind" help:"Bind volume to host path (name or name=path)"`
	AutoDetectFlags `embed:""`
}

func (c *StartCmd) Run() error {
	// Remote refs (@github.com/...) are handled exclusively by `charly box pull`.
	if IsRemoteImageRef(StripURLScheme(c.Box)) {
		return fmt.Errorf("remote refs are not accepted here; run 'charly box pull %s' first, then 'charly start <image-name>'", c.Box)
	}
	c.Box, c.Instance = canonicalizeDeployArg(c.Box, c.Instance)
	if err := rejectImageRefAsDeployName(c.Box); err != nil {
		return err
	}

	// Resource arbitration: starting a pod deploy that claims requires_exclusive
	// preempts the running holders of that resource (persistent lease —
	// released by `charly stop`/`charly remove`). No-op when gated by an outer
	// orchestrator or when this deploy claims nothing exclusive. See
	// charly/preempt.go.
	if dc := loadDeployConfigForRead("charly start"); dc != nil {
		key := deployKey(c.Box, c.Instance)
		if node, ok := dc.Bundle[key]; ok {
			// Resource arbitration: an EXCLUSIVE claim (sole use — a VM) preempts
			// holders + shared pods; a SHARED claim (refcounted — a GPU shared
			// across pods via CDI) flips the resource into shared mode (GPU ->
			// nvidia + CDI) BEFORE device detection below, so the quadlet/run
			// picks the GPU up. Persistent lease (released by stop/remove). See
			// charly/preempt.go.
			if _, perr := acquireResourceForClaimant(key, node, false); perr != nil {
				return perr
			}
		}
	}

	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}

	if rt.RunMode == "quadlet" {
		return c.runQuadlet(rt)
	}

	return c.runDirect(rt)
}

func (c *StartCmd) runDirect(rt *ResolvedRuntime) error {
	var detected DetectedDevices
	if !c.NoAutoDetect {
		detected = DetectHostDevices()
		LogDetectedDevices(detected)
	}

	engine := rt.RunEngine

	// Ensure NVIDIA CDI specs exist for nested container GPU access
	if detected.GPU && engine == "podman" {
		EnsureCDI()
	}

	// Load charly.yml for volume backing config + later use (env merge,
	// sidecar check, agent forwarding, metadata overlay).
	dc := loadDeployConfigForRead("charly start")
	var deployVolumes []DeployVolumeConfig
	if overlay, ok := dc.Lookup(c.Box, c.Instance); ok {
		deployVolumes = overlay.Volume
	}

	// Resolve the deploy key to its declared image short-name via THE shared
	// resolver (deploy.go) — the same one charly config / shell / check live use,
	// so no command diverges when key != image (kind:check beds, Pattern B).
	// c.Image stays the deploy-KEY for container / quadlet / overlay lookups;
	// only the image ref uses the resolved name.
	deployBoxName := resolveDeployBoxName(c.Box, c.Instance)
	// Resolve from image labels (+ charly.yml overlay). No charly.yml.
	imageRef := resolveShellImageRef("", deployBoxName, c.Tag)
	if err := EnsureImage(imageRef, rt); err != nil {
		return err
	}
	meta, err := ExtractMetadata(engine, imageRef)
	if err != nil {
		return err
	}
	if meta == nil {
		return fmt.Errorf("image %s has no embedded metadata; rebuild with latest charly", imageRef)
	}
	engine = ResolveBoxEngineFromMeta(meta, rt.RunEngine)
	MergeDeployOntoMetadata(meta, dc, c.Box, c.Instance)

	// Sidecars require quadlet mode (pod networking is only available via quadlet)
	if overlay, ok := dc.Lookup(c.Box, c.Instance); ok && len(overlay.Sidecar) > 0 {
		return fmt.Errorf("image %s has sidecars configured in charly.yml; use 'charly config %s && charly start %s' (sidecars require quadlet mode)", c.Box, c.Box, c.Box)
	}

	uid := meta.UID
	gid := meta.GID
	home := meta.Home
	ports := meta.Port
	security := meta.Security
	network := meta.Network
	entrypoint := resolveEntrypointFromMeta(meta)

	cliVolumes := parseVolumeFlagsStandalone(c.VolumeFlag, c.Bind)
	volumes, bindMounts := ResolveVolumeBacking(c.Box, c.Instance, meta.Volume, mergeVolumeConfigs(deployVolumes, cliVolumes), meta.Home, rt.EncryptedStoragePath, rt.VolumesPath)

	envAccepts := meta.EnvAccept
	envRequires := meta.EnvRequire
	if meta.Registry != "" {
		imageRef = resolveShellImageRef(meta.Registry, deployBoxName, c.Tag)
	}

	// Auto-initialize and mount encrypted volumes if needed
	if err := ensureEncryptedMounts(c.Box, c.Instance, false); err != nil {
		return err
	}

	// Verify bind mounts
	if err := verifyBindMounts(bindMounts, c.Box); err != nil {
		return err
	}

	// Resolve env vars from labels
	deployEnv := meta.Env
	var deployEnvFile string
	startCtrName := containerNameInstance(c.Box, c.Instance)
	startAccepted := AcceptedEnvSet(envAccepts, envRequires)
	startGlobalEnv := dc.GlobalEnvForImage(deployKey(c.Box, c.Instance), startCtrName, startAccepted)
	envVars, err := ResolveEnvVars(startGlobalEnv, deployEnv, deployEnvFile, workspaceBindHost(bindMounts), c.EnvFile, c.Env)
	if err != nil {
		return err
	}

	// Merge auto-detected devices into security config
	if !security.Privileged {
		security.Devices = appendUnique(security.Devices, detected.Devices...)
		if detected.AMDGPU {
			security.GroupAdd = appendGroupsForAMDGPU(security.GroupAdd)
		}
	}
	envVars = appendAutoDetectedEnv(envVars, detected)

	// Resolve network (default to shared "charly" network)
	resolvedNetwork, netErr := ResolveNetwork(network, engine)
	if netErr != nil {
		return netErr
	}

	// Apply port overrides from --port flags and persist to charly.yml
	if len(c.Port) > 0 {
		ports, err = ApplyPortOverrides(ports, c.Port)
		if err != nil {
			return err
		}
		// SetPorts: true because this branch only runs when len(c.Port)>0
		// (operator explicitly passed --port flags via charly start). Per
		// the 2026-05-09 SaveDeployStateInput.SetPorts contract, ports
		// are written ONLY on explicit operator opt-in.
		saveDeployState(c.Box, c.Instance, SaveDeployStateInput{
			Ports:    ports,
			SetPorts: true,
		})
	}

	// Pre-flight port conflict check
	if conflicts := CheckPortAvailability(ports, rt.BindAddress, engine); len(conflicts) > 0 {
		return fmt.Errorf("port conflicts detected:%s", FormatPortConflicts(conflicts, c.Box))
	}

	// Inject agent forwarding mounts and env (direct mode only)
	var deployBox *BundleNode
	if overlay, ok := dc.Lookup(c.Box, c.Instance); ok {
		deployBox = &overlay
	}
	agentFwd := ResolveAgentForwarding(rt, deployBox, home)
	for _, v := range agentFwd.Volumes {
		security.Mounts = appendUnique(security.Mounts, v)
	}
	envVars = append(envVars, agentFwd.Env...)

	name := containerNameInstance(c.Box, c.Instance)
	workDir := resolveWorkingDir(volumes, bindMounts, home, c.Box, c.Instance)
	args := buildStartArgs(engine, imageRef, uid, gid, ports, name, volumes, bindMounts, detected.GPU, rt.BindAddress, envVars, security, entrypoint, workDir, resolvedNetwork)

	cmd := exec.Command(args[0], args[1:]...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s run failed: %w\n%s", EngineBinary(engine), err, strings.TrimSpace(string(output)))
	}

	containerID := strings.TrimSpace(string(output))
	if len(containerID) > 12 {
		containerID = containerID[:12]
	}
	fmt.Println(containerID)
	fmt.Fprintf(os.Stderr, "Started %s as %s\n", name, containerID)

	// Start tunnel if configured (charly.yml-only; labels never carry tunnel).
	if meta.Tunnel != nil {
		tc := TunnelConfigFromMetadata(meta)
		if tc != nil {
			if err := TunnelStart(*tc); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: tunnel setup failed: %v\n", err)
			}
		}
	}

	return nil
}

func (c *StartCmd) runQuadlet(_ *ResolvedRuntime) error {
	exists, err := quadletExistsInstance(c.Box, c.Instance)
	if err != nil {
		return err
	}

	// Direct-mode deploy (no quadlet, but marker file recorded by
	// runConfigDirect). Fall through to `podman start charly-<name>` instead
	// of `systemctl --user start`. Encrypted-volume mounts are skipped
	// in direct mode (those require systemd-run --scope, see
	// runConfigDirect's warning path).
	if !exists && IsDirectDeploy(c.Box, c.Instance) {
		name := containerNameInstance(c.Box, c.Instance)
		cmd := exec.Command("podman", "start", name)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("starting %s (direct mode): %w", name, err)
		}
		fmt.Fprintf(os.Stderr, "Started %s (direct mode)\n", name)
		return nil
	}

	if !exists {
		return fmt.Errorf("not configured; run 'charly config %s' first", c.Box)
	}

	// Mount encrypted volumes if needed (runtime concern, not config)
	if err := ensureEncryptedMounts(c.Box, c.Instance, false); err != nil {
		return err
	}

	svc := serviceNameInstance(c.Box, c.Instance)
	cmd := exec.Command("systemctl", "--user", "start", svc)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("starting %s: %w", svc, err)
	}
	fmt.Fprintf(os.Stderr, "Started %s\n", svc)
	return nil
}

// hasConfigOverrides returns true if the user passed any config flags that
// should trigger quadlet regeneration (port maps, env vars, env file).
// StopCmd stops a running container started by StartCmd
type StopCmd struct {
	Box      string `arg:"" help:"Box name or remote ref"`
	Instance string `short:"i" long:"instance" help:"Instance name for running multiple containers of the same box"`
	Unmount  bool   `long:"unmount" help:"After stopping, also tear down encrypted FUSE mounts and gocryptfs scope units (charly-enc-<box>-<volume>.scope) for this box"`
}

func (c *StopCmd) Run() error {
	c.Box, c.Instance = canonicalizeDeployArg(c.Box, c.Instance)
	// Releasing a persistent exclusive claim restores any holder this deploy
	// preempted (no-op if no lease / gated by an outer orchestrator).
	defer releaseResourceClaim(deployKey(c.Box, c.Instance))
	// Resolve the image name (handle remote refs)
	boxName := c.Box
	ref := StripURLScheme(c.Box)
	if IsRemoteImageRef(ref) {
		boxName = ParseRemoteRef(ref).Name
	}

	// Stop tunnel before stopping container (best-effort)
	stopTunnelForImage(boxName, c.Instance)

	if err := stopPodService(boxName, c.Instance); err != nil {
		return err
	}

	stopUnmountIfRequested(c.Unmount, boxName, c.Instance)
	return nil
}

// stopPodService stops a running pod deployment — the quadlet service when
// one exists (always via systemctl, so podman-stop + Restart=always can't
// create a restart loop), else the container directly via the resolved engine
// with a fallback to the other engine. It performs NO tunnel/unmount side
// effects — callers layer those on. Shared by StopCmd.Run and the resource
// arbiter (charly/preempt.go), whose preemption path wants a bare, reversible
// service stop that leaves the holder's disk/container intact for restart.
func stopPodService(boxName, instance string) error {
	quadletActive, _ := quadletExistsInstance(boxName, instance)
	if quadletActive {
		svc := serviceNameInstance(boxName, instance)
		cmd := exec.Command("systemctl", "--user", "stop", svc)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("stopping %s: %w", svc, err)
		}
		fmt.Fprintf(os.Stderr, "Stopped %s\n", svc)
		return nil
	}

	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}
	runEngine := ResolveBoxEngineForDeploy(boxName, instance, rt.RunEngine)
	engine := EngineBinary(runEngine)
	name := containerNameInstance(boxName, instance)

	cmd := exec.Command(engine, "stop", name)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Fallback: try the other engine if the container wasn't found
		otherEngine := "docker"
		if runEngine == "docker" {
			otherEngine = "podman"
		}
		otherBinary := EngineBinary(otherEngine)
		fallbackCmd := exec.Command(otherBinary, "stop", name)
		if _, fallbackErr := fallbackCmd.CombinedOutput(); fallbackErr == nil {
			fmt.Fprintf(os.Stderr, "Stopped %s (via %s)\n", name, otherEngine)
			return nil
		}
		return fmt.Errorf("%s stop failed: %w\n%s", engine, err, strings.TrimSpace(string(output)))
	}

	fmt.Fprintf(os.Stderr, "Stopped %s\n", name)
	return nil
}

// startPodService starts an already-configured pod deployment — the quadlet
// service when one exists, else the existing stopped container via the
// resolved engine. Used by the resource arbiter to restore a preempted holder:
// the deployment's quadlet/container already exists (the holder was running
// before preemption), so this is a plain service/container start, not a full
// `charly start` re-config.
func startPodService(boxName, instance string) error {
	quadletActive, _ := quadletExistsInstance(boxName, instance)
	if quadletActive {
		svc := serviceNameInstance(boxName, instance)
		cmd := exec.Command("systemctl", "--user", "start", svc)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("starting %s: %w", svc, err)
		}
		fmt.Fprintf(os.Stderr, "Started %s\n", svc)
		return nil
	}

	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}
	runEngine := ResolveBoxEngineForDeploy(boxName, instance, rt.RunEngine)
	engine := EngineBinary(runEngine)
	name := containerNameInstance(boxName, instance)

	cmd := exec.Command(engine, "start", name)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s start failed: %w\n%s", engine, err, strings.TrimSpace(string(output)))
	}
	fmt.Fprintf(os.Stderr, "Started %s\n", name)
	return nil
}

// stopUnmountIfRequested tears down encrypted-volume FUSE mounts after a
// successful stop, when the user passed --unmount. Best-effort by design
// (matches the stopTunnelForImage pattern); failures emit a warning but
// don't propagate, since the container has already stopped and the user
// can retry the unmount manually with `charly config unmount <image>`.
func stopUnmountIfRequested(want bool, boxName, instance string) {
	if !want {
		return
	}
	if err := encUnmount(boxName, instance, ""); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: encrypted-volume unmount failed: %v\n", err)
	}
}

// RestartCmd restarts a service container. In quadlet mode it issues a single
// `systemctl --user restart`, which is atomic from systemd's perspective —
// ExecStopPost (e.g. tailscale serve --off) runs before ExecStartPost
// (tailscale serve), and the unit ends in either active or failed, never the
// silent stopped state that a manual stop+start sequence can produce when
// start fails.
type RestartCmd struct {
	Box      string `arg:"" help:"Box name or remote ref"`
	Instance string `short:"i" long:"instance" help:"Instance name for running multiple containers of the same box"`
}

func (c *RestartCmd) Run() error {
	boxName := c.Box
	ref := StripURLScheme(c.Box)
	if IsRemoteImageRef(ref) {
		boxName = ParseRemoteRef(ref).Name
	}

	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}

	quadletActive, _ := quadletExistsInstance(boxName, c.Instance)
	if quadletActive {
		svc := serviceNameInstance(boxName, c.Instance)
		cmd := exec.Command("systemctl", "--user", "restart", svc)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("restarting %s: %w", svc, err)
		}
		fmt.Fprintf(os.Stderr, "Restarted %s\n", svc)
		return nil
	}

	// Direct mode: delegate to engine restart.
	runEngine := ResolveBoxEngineForDeploy(boxName, c.Instance, rt.RunEngine)
	engine := EngineBinary(runEngine)
	name := containerNameInstance(boxName, c.Instance)

	cmd := exec.Command(engine, "restart", name)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s restart %s failed: %w\n%s", engine, name, err, strings.TrimSpace(string(output)))
	}
	fmt.Fprintf(os.Stderr, "Restarted %s\n", name)
	return nil
}

// stopTunnelForImage attempts to stop any tunnel for the given image (best-effort).
func stopTunnelForImage(boxName, instance string) {
	var tc *TunnelConfig

	// Tunnel config comes from charly.yml (overlaid onto BoxMetadata).
	ctrName := containerNameInstance(boxName, instance)
	imageRef := containerImage("podman", ctrName)
	if imageRef != "" {
		meta, metaErr := ExtractMetadata("podman", imageRef)
		if metaErr == nil && meta != nil {
			dc := loadDeployConfigForRead("charly start tunnel merge")
			MergeDeployOntoMetadata(meta, dc, boxName, instance)
			if meta.Tunnel != nil {
				tc = TunnelConfigFromMetadata(meta)
			}
		}
	}

	if tc != nil {
		if err := TunnelStop(*tc); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: tunnel teardown failed: %v\n", err)
		}
	}
}

// buildStartArgs constructs the container run argument list for a detached service.
// entrypoint is the init system command (e.g., ["supervisord", "-n", "-c", "/etc/supervisord.conf"])
// or the fallback (e.g., ["sleep", "infinity"]).
func buildStartArgs(engine, imageRef string, uid, gid int, ports []string, name string, volumes []VolumeMount, bindMounts []ResolvedBindMount, gpu bool, bindAddr string, envVars []string, security SecurityConfig, entrypoint []string, workingDir string, network ...string) []string {
	binary := EngineBinary(engine)
	args := []string{
		binary, "run", "-d", "--rm",
		"--name", name,
		"-w", workingDir,
	}
	if len(network) > 0 && network[0] != "" {
		args = append(args, "--network", network[0])
	}
	if gpu {
		args = append(args, GPURunArgs(engine)...)
	}
	args = append(args, SecurityArgs(security)...)
	for _, port := range ports {
		args = append(args, "-p", localizePort(port, bindAddr))
	}
	for _, vol := range volumes {
		args = append(args, "-v", fmt.Sprintf("%s:%s", vol.VolumeName, vol.ContainerPath))
	}
	for _, bm := range bindMounts {
		args = append(args, "-v", fmt.Sprintf("%s:%s", bm.HostPath, bm.ContPath))
	}
	for _, m := range security.Mounts {
		if after, ok := strings.CutPrefix(m, "tmpfs:"); ok {
			// tmpfs:/path:options → --tmpfs /path:options
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
	args = append(args, imageRef)
	args = append(args, entrypoint...)
	return args
}

// resolveEntrypointFromMeta determines the entrypoint from image metadata (runtime mode).
// Label-first: the build-resolved init contract is baked into the
// ai.opencharly.init_def label (meta.InitDef), so any init system declared in
// the embedded `init:` vocabulary — including custom ones — now reaches
// runtime. wellKnownInitDefs is consulted only for pre-init_def-label images
// (built before the label existed; their labels cannot be re-baked).
func resolveEntrypointFromMeta(meta *BoxMetadata) []string {
	if meta.Init == "" {
		return []string{"sleep", "infinity"}
	}
	if meta.InitDef != nil {
		// The baked entrypoint is authoritative. An empty entrypoint means
		// the container boots via the image's own init (systemd-on-bootc),
		// exactly as the legacy registry encoded — fall through to the
		// image default rather than overriding with sleep infinity.
		return meta.InitDef.Entrypoint
	}
	if def, ok := wellKnownInitDefs[meta.Init]; ok {
		return def.Entrypoint
	}
	return []string{"sleep", "infinity"}
}
