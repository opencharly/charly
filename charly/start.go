package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// start.go — the pod-lifecycle HOST-side resolution the CLI-struct port (DEPLOY wave) left
// behind. StartCmd/StopCmd/RestartCmd moved to candy/plugin-pod (command:start/stop/restart);
// restart is pure sdk/deploykit logic (deploykit.RestartPodService) needing no host seam, but
// start/stop are registry-bound (ResolveTarget, the plugin loader — a core Mechanism a plugin
// cannot hold), so they reach this file's resolvers via HostBuild("pod-start")/HostBuild(
// "pod-stop") (host_build_pod_start.go / host_build_pod_stop.go), which reconstruct the moved
// commands' bodies verbatim as podStartCmd/podStopCmd below. TRACKED P13-KERNEL EXIT: this
// resolution kernel (buildStartArgs/resolveEntrypointFromMeta/stopTunnelForImage +
// startViaLifecycle/stopViaLifecycle in pod_lifecycle_verb.go) is registered P13-KERNEL migration
// inventory — it moves through the ONE venue-scoped-executor-session seam alongside bundle's
// deploy-add/deploy-del resolver kernel when that wave builds it (R3 across waves, never two
// seams), never a permanent core residence.

// podStartCmd is the host-side reconstruction of the former StartCmd (now command:start in
// candy/plugin-pod) — hostBuildPodStart (host_build_pod_start.go) runs its Run() body VERBATIM.
type podStartCmd struct {
	Box          string
	Tag          string
	Build        bool
	Env          []string
	EnvFile      string
	Instance     string
	Port         []string
	VolumeFlag   []string
	Bind         []string
	NoAutoDetect bool
}

func (c *podStartCmd) Run() error {
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

// podStopCmd is the host-side reconstruction of the former StopCmd (now command:stop in
// candy/plugin-pod) — hostBuildPodStop (host_build_pod_stop.go) runs its Run() body VERBATIM.
type podStopCmd struct {
	Box      string
	Instance string
	Unmount  bool
}

func (c *podStopCmd) Run() error {
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
