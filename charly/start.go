package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/opencharly/sdk/deploykit"
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
	c.Box, c.Instance = canonicalizeDeployArg(c.Box, c.Instance)
	// Resolve the image name (handle remote refs)
	boxName := c.Box
	ref := StripURLScheme(c.Box)
	if IsRemoteImageRef(ref) {
		boxName = ParseRemoteRef(ref).Name
	}
	// Unified dispatch (the K4 deep-body move): `charly stop` routes through LifecycleTarget.Stop —
	// a pod reaches the plugin's OpStop body (tunnel stop → container stop → enc unmount if
	// --unmount); the shared arbiter claim is RELEASED host-side by the F6 dispatch after OpStop
	// (restoring any holder this deploy preempted). --unmount rides the ctx into the plan hook.
	return stopViaLifecycle(boxName, c.Instance, c.Unmount)
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

// buildStartArgs MOVED to sdk/deploykit as BuildStartArgs (K4 lane B — the direct-mode `charly
// start` argv builder now lives with pod_lifecycle_resolve.go's move to candy/plugin-deploy-pod;
// charly core no longer calls it — config_image.go renders quadlet units instead).

// resolveEntrypointFromMeta MOVED to sdk/kit (K4 lane B); see kit_aliases.go's
// resolveEntrypointFromMeta = kit.ResolveEntrypointFromMeta.
