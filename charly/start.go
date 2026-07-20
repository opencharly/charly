package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
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
	if spec.IsRemoteImageRef(kit.StripURLScheme(c.Box)) {
		return fmt.Errorf("remote refs are not accepted here; run 'charly box pull %s' first, then 'charly start <image-name>'", c.Box)
	}
	c.Box, c.Instance = deploykit.CanonicalizeDeployArg(c.Box, c.Instance)
	if err := deploykit.RejectImageRefAsDeployName(c.Box); err != nil {
		return err
	}
	// Unified dispatch (the K4 deep-body move): `charly start` routes through ResolveTarget →
	// LifecycleTarget.Start — a pod reaches the plugin's OpStart body (the podman/systemctl start +
	// enc/tunnel compose) over F6, with the shared arbiter claim BRACKETED host-side (acquire before
	// OpStart / release on failure). The direct-mode CLI extras ride podStartOpts into the plan hook.
	return startViaLifecycle(c.Box, c.Instance, podStartOpts{
		Env:          c.Env,
		EnvFile:      c.EnvFile,
		Port:         c.Port,
		VolumeFlag:   c.VolumeFlag,
		Bind:         c.Bind,
		NoAutoDetect: c.NoAutoDetect,
	})
}

// StopCmd stops a running container started by StartCmd
type StopCmd struct {
	Box      string `arg:"" help:"Box name or remote ref"`
	Instance string `short:"i" long:"instance" help:"Instance name for running multiple containers of the same box"`
	Unmount  bool   `long:"unmount" help:"After stopping, also tear down encrypted FUSE mounts and gocryptfs scope units (charly-enc-<box>-<volume>.scope) for this box"`
}

func (c *StopCmd) Run() error {
	c.Box, c.Instance = deploykit.CanonicalizeDeployArg(c.Box, c.Instance)
	// Resolve the image name (handle remote refs)
	boxName := c.Box
	ref := kit.StripURLScheme(c.Box)
	if spec.IsRemoteImageRef(ref) {
		boxName = spec.ParseRemoteRef(ref).Name
	}
	// Unified dispatch (the K4 deep-body move): `charly stop` routes through LifecycleTarget.Stop —
	// a pod reaches the plugin's OpStop body (tunnel stop → container stop → enc unmount if
	// --unmount); the shared arbiter claim is RELEASED host-side by the F6 dispatch after OpStop
	// (restoring any holder this deploy preempted). --unmount rides the ctx into the plan hook.
	return stopViaLifecycle(boxName, c.Instance, c.Unmount)
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
	ref := kit.StripURLScheme(c.Box)
	if spec.IsRemoteImageRef(ref) {
		boxName = spec.ParseRemoteRef(ref).Name
	}

	rt, err := kit.ResolveRuntime()
	if err != nil {
		return err
	}

	quadletActive, _ := kit.QuadletExistsInstance(boxName, c.Instance)
	if quadletActive {
		svc := kit.ServiceNameInstance(boxName, c.Instance)
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
	engine := kit.EngineBinary(runEngine)
	name := kit.ContainerNameInstance(boxName, c.Instance)

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
	ctrName := kit.ContainerNameInstance(boxName, instance)
	imageRef := containerImage("podman", ctrName)
	if imageRef != "" {
		meta, metaErr := ExtractMetadata("podman", imageRef)
		if metaErr == nil && meta != nil {
			dc := deploykit.LoadDeployConfigForRead("charly start tunnel merge")
			deploykit.MergeDeployOntoMetadata(meta, dc, boxName, instance)
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
func buildStartArgs(engine, imageRef string, uid, gid int, ports []string, name string, volumes []deploykit.VolumeMount, bindMounts []deploykit.ResolvedBindMount, gpu bool, bindAddr string, envVars []string, security SecurityConfig, entrypoint []string, workingDir string, network ...string) []string {
	binary := kit.EngineBinary(engine)
	args := []string{
		binary, "run", "-d", "--rm",
		"--name", name,
		"-w", workingDir,
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
