package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// StatusCmd is defined in status.go

// LogsCmd shows service container logs
type LogsCmd struct {
	Box      string `arg:"" help:"Box name or remote ref"`
	Follow   bool   `short:"f" long:"follow" help:"Follow log output"`
	Instance string `short:"i" long:"instance" help:"Instance name for running multiple containers of the same box"`
	Sidecar  string `long:"sidecar" help:"Show the named SIDECAR container's logs instead of the app container's"`
}

func (c *LogsCmd) Run() error {
	c.Box, c.Instance = canonicalizeDeployArg(c.Box, c.Instance)
	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}

	boxName := resolveBoxName(c.Box)

	if rt.RunMode == "quadlet" {
		svc := serviceNameInstance(boxName, c.Instance)
		if c.Sidecar != "" {
			svc = SidecarContainerNameInstance(boxName, c.Instance, c.Sidecar) + ".service"
		}
		args := []string{"--user", "-u", svc}
		if c.Follow {
			args = append(args, "-f")
		}
		cmd := exec.Command("journalctl", args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("journalctl failed: %w", err)
		}
		return nil
	}

	// Resolve per-image engine from charly.yml
	runEngine := ResolveBoxEngineForDeploy(boxName, c.Instance, rt.RunEngine)
	engine := EngineBinary(runEngine)
	name := containerNameInstance(boxName, c.Instance)
	if c.Sidecar != "" {
		name = SidecarContainerNameInstance(boxName, c.Instance, c.Sidecar)
	}
	args := []string{"logs"}
	if c.Follow {
		args = append(args, "-f")
	}
	args = append(args, name)
	cmd := exec.Command(engine, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s logs failed: %w", engine, err)
	}
	return nil
}

// UpdateCmd updates an image (pulls/builds the latest), preserves the
// existing deploy config (user-overlay state untouched), and restarts
// the service to pick up the new image.
//
// This verb handles the destroy-free update path for every target. The
// first arg accepts EITHER a deploy name (looked up in charly.yml —
// VM/local/pod targets all dispatch from here) OR a bare image name
// (for direct image updates not tied to a deploy).
//
// Key semantic: this verb NEVER calls `charly bundle add` to regenerate
// the user-overlay deploy
// entry. User-overlay configuration (port overrides, volume bindings,
// env, tunnel) is preserved across updates. Per the user's directive:
// "Any config changes should be done via charly config only" — this verb
// updates ARTIFACTS, charly config updates CONFIG.
type UpdateCmd struct {
	Box       string `arg:"" help:"Deploy name (resolved via charly.yml) OR box name. For deploys, the target's update strategy is auto-selected (pod=systemctl restart with new image; vm=in-guest candy re-apply; local=idempotent re-apply)."`
	Tag       string `long:"tag" help:"Image CalVer tag (empty = newest local CalVer resolved via the ai.opencharly.version OCI label)"`
	Build     bool   `long:"build" help:"Force local build instead of pulling from registry"`
	Instance  string `short:"i" long:"instance" help:"Instance name for running multiple containers of the same box"`
	Seed      bool   `long:"seed" default:"true" negatable:"" help:"Sync data from new image into bind-backed volumes (default: true)"`
	ForceSeed bool   `long:"force-seed" help:"Overwrite existing data in volumes (default: only add new files)"`
	DataFrom  string `long:"data-from" help:"Sync data from this data image instead"`
}

// Run dispatches `charly update <name>` to the target-specific update
// helper. The argument MUST resolve to a deploy entry in charly.yml
// (project + user-overlay merged). There is NO legacy fall-through to
// "treat the argument as an image name" — to refresh an image artifact
// without restarting any deploy, use `charly box pull <name>`.
//
// The dispatch keeps ZERO duplicate code paths and ZERO silent
// fallbacks. Every branch fails fast with an actionable error message.
func (c *UpdateCmd) Run() error {
	if IsRemoteImageRef(StripURLScheme(c.Box)) {
		return fmt.Errorf("remote refs are not accepted here; run 'charly box pull %s' first", c.Box)
	}
	c.Box, c.Instance = canonicalizeDeployArg(c.Box, c.Instance)
	return c.dispatchByDeployTarget()
}

// RemoveCmd removes a service container
type RemoveCmd struct {
	Box        string   `arg:"" help:"Box name or remote ref"`
	Instance   string   `short:"i" long:"instance" help:"Instance name for running multiple containers of the same box"`
	Purge      bool     `long:"purge" help:"Also remove named volumes"`
	KeepDeploy bool     `name:"keep-deploy" help:"Keep charly.yml entry for this box"`
	Env        []string `short:"e" long:"env" sep:"none" help:"Set env var for hooks (KEY=VALUE)"`
}

func (c *RemoveCmd) Run() error {
	c.Box, c.Instance = canonicalizeDeployArg(c.Box, c.Instance)
	// Releasing a persistent exclusive claim restores any holder this deploy
	// preempted (no-op if no lease / gated by an outer orchestrator).
	defer releaseResourceClaim(deployKey(c.Box, c.Instance))
	boxName := resolveBoxName(c.Box)

	// Stop tunnel before removing container (best-effort)
	stopTunnelForImage(boxName, c.Instance)

	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}

	// Resolve per-image engine from charly.yml
	runEngine := ResolveBoxEngineForDeploy(boxName, c.Instance, rt.RunEngine)
	engine := EngineBinary(runEngine)
	containerName := containerNameInstance(boxName, c.Instance)

	// Run pre_remove hooks (best-effort, before stopping)
	c.runPreRemoveHook(engine, containerName, boxName)

	if rt.RunMode == "quadlet" {
		svc := serviceNameInstance(boxName, c.Instance)
		stop := exec.Command("systemctl", "--user", "stop", svc)
		_ = stop.Run()

		qdir, err := quadletDir()
		if err != nil {
			return err
		}

		qpath := filepath.Join(qdir, quadletFilenameInstance(boxName, c.Instance))
		if err := os.Remove(qpath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removing quadlet file: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Removed %s\n", qpath)

		// Remove pod file if it exists (sidecar mode)
		podPath := filepath.Join(qdir, podQuadletFilenameInstance(boxName, c.Instance))
		if err := os.Remove(podPath); err == nil {
			fmt.Fprintf(os.Stderr, "Removed %s\n", podPath)
		}

		// Remove sidecar .container files (exact-name match, no prefix
		// glob). Sources sidecar names from charly.yml — see
		// resolveSidecarNames for why charly.yml is authoritative.
		sidecarNames := resolveSidecarNames(boxName, c.Instance)
		podBase := PodNameInstance(boxName, c.Instance)
		for _, sc := range sidecarNames {
			scPath := filepath.Join(qdir, podBase+"-"+sc+".container")
			if err := os.Remove(scPath); err == nil {
				fmt.Fprintf(os.Stderr, "Removed %s\n", scPath)
			}
		}

		// Remove sidecar config files. Naming convention is
		// `<podBase>-<sidecar>-<purpose>.<ext>` (e.g.
		// charly-foo-tailscale-serve.json). The prefix is
		// anchored to the sidecar NAME so unrelated sidecars / bases
		// can't match.
		if scDir, scErr := sidecarConfigDir(); scErr == nil {
			if entries, err := os.ReadDir(scDir); err == nil {
				for _, sc := range sidecarNames {
					scfPrefix := podBase + "-" + sc + "-"
					for _, entry := range entries {
						if strings.HasPrefix(entry.Name(), scfPrefix) {
							scfPath := filepath.Join(scDir, entry.Name())
							if err := os.Remove(scfPath); err == nil {
								fmt.Fprintf(os.Stderr, "Removed %s\n", scfPath)
							}
						}
					}
				}
			}
		}

		// Stop companion services before removing (best-effort)
		stopTunnel := exec.Command("systemctl", "--user", "stop", tunnelServiceFilename(boxName))
		_ = stopTunnel.Run()
		stopEnc := exec.Command("systemctl", "--user", "stop", encServiceFilename(boxName))
		_ = stopEnc.Run()

		svcDir, svcDirErr := systemdUserDir()
		if svcDirErr == nil {
			tunnelPath := filepath.Join(svcDir, tunnelServiceFilename(boxName))
			if err := os.Remove(tunnelPath); err == nil {
				fmt.Fprintf(os.Stderr, "Removed %s\n", tunnelPath)
			}
			encPath := filepath.Join(svcDir, encServiceFilename(boxName))
			if err := os.Remove(encPath); err == nil {
				fmt.Fprintf(os.Stderr, "Removed %s\n", encPath)
			}
		}

		cmd := exec.Command("systemctl", "--user", "daemon-reload")
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("systemctl daemon-reload failed: %w\n%s", err, strings.TrimSpace(string(output)))
		}

		fmt.Fprintf(os.Stderr, "Reloaded systemd user daemon\n")

		// Clear any lingering failed state for main + companion services (best-effort)
		for _, unit := range []string{
			svc,
			tunnelServiceFilename(boxName),
			encServiceFilename(boxName),
		} {
			rf := exec.Command("systemctl", "--user", "reset-failed", unit)
			_ = rf.Run()
		}

		if c.Purge {
			removeVolumes(engine, boxName, c.Instance)
		}
		if !c.KeepDeploy {
			cleanDeployEntry(boxName, c.Instance)
		}
		return nil
	}

	// Direct mode: stop + rm
	name := containerNameInstance(boxName, c.Instance)

	stop := exec.Command(engine, "stop", name)
	_ = stop.Run()

	rm := exec.Command(engine, "rm", name)
	_ = rm.Run()

	fmt.Fprintf(os.Stderr, "Removed container %s\n", name)

	if c.Purge {
		removeVolumes(engine, boxName, c.Instance)
	}
	if !c.KeepDeploy {
		cleanDeployEntry(boxName, c.Instance)
	}
	return nil
}

// runPreRemoveHook runs pre_remove hooks (best-effort). Reads hooks from
// the running container's OCI labels.
func (c *RemoveCmd) runPreRemoveHook(engine, containerName, boxName string) {
	imageRef := containerImage(engine, containerName)
	if imageRef == "" {
		return
	}
	meta, metaErr := ExtractMetadata(engine, imageRef)
	if metaErr != nil || meta == nil || meta.Hook == nil || meta.Hook.PreRemove == "" {
		return
	}
	// Pass credential-backed secrets (secret_accept/require) to the hook
	// explicitly — scrubbed from c.Env, not reliably inherited via podman exec.
	hookEnv := append(append([]string{}, c.Env...), resolveHookSecretEnv(boxName, c.Instance, meta)...)
	if err := RunHook(engine, containerName, meta.Hook.PreRemove, hookEnv); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: pre_remove hook failed: %v\n", err)
	}
}

// containerImageRef returns the image ref backing a running container
// (.Config.Image via `<engine> inspect`). THE single container→image-ref
// inspector — used wherever a command must read what a LIVE container is
// actually running (mcp probes, service init detection, remove hooks,
// direct-mode start). containerImage is the best-effort (""-on-error)
// wrapper over it, so there is exactly one inspect implementation.
func containerImageRef(engine, containerName string) (string, error) {
	out, _, exit, err := runCaptureCmd(exec.Command(EngineBinary(engine), "inspect", "--format", "{{.Config.Image}}", containerName))
	if err != nil {
		return "", fmt.Errorf("inspecting container %s: %w", containerName, err)
	}
	if exit != 0 {
		return "", fmt.Errorf("inspect %s: exit %d", containerName, exit)
	}
	return strings.TrimSpace(out), nil
}

// containerImage returns the image ref for a running container, best-effort
// ("" on error). Thin wrapper over containerImageRef.
func containerImage(engine, containerName string) string {
	ref, _ := containerImageRef(engine, containerName)
	return ref
}

// resolveBoxName extracts the short box name from a ref that may be
// a local box name or a remote ref (github.com/org/repo/box[@version]).
func resolveBoxName(box string) string {
	ref := StripURLScheme(box)
	if IsRemoteImageRef(ref) {
		return ParseRemoteRef(ref).Name
	}
	return box
}

// resolveSidecarNames returns the sorted set of sidecar key names
// attached to this deploy via charly.yml. charly.yml is the
// authoritative source because sidecars only become attached via
// `charly config --sidecar <name>` which writes them into the deploy
// entry's `sidecar:` map. Image OCI labels carry sidecar TEMPLATES
// but not "which sidecars are attached to THIS deploy on THIS host".
// Returns nil when nothing is attached.
func resolveSidecarNames(boxName, instance string) []string {
	dc, err := LoadBundleConfig()
	if err != nil || dc == nil {
		return nil
	}
	entry, ok := dc.Bundle[deployKey(boxName, instance)]
	if !ok || len(entry.Sidecar) == 0 {
		return nil
	}
	names := make([]string, 0, len(entry.Sidecar))
	for name := range entry.Sidecar {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
