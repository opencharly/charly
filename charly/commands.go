package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// podLogsCmd is the host-side reconstruction of the former LogsCmd (now command:logs in
// candy/plugin-pod) — hostBuildPodLogs (host_build_pod_logs.go) runs its Run() body VERBATIM.
// TRACKED P13-KERNEL EXIT: dispatchLifecycleTarget/LogsOpts/LifecycleTarget (deploy_target_unified.go,
// pod_lifecycle_verb.go) are registered P13-KERNEL migration inventory (see start.go's header) —
// this resolver moves through the same venue-scoped-executor-session seam when that wave lands.
type podLogsCmd struct {
	Box      string
	Follow   bool
	Instance string
	Sidecar  string
}

func (c *podLogsCmd) Run() error {
	c.Box, c.Instance = deploykit.CanonicalizeDeployArg(c.Box, c.Instance)
	// `charly logs` routes through the unified LifecycleTarget → OpLogs (F12): the host resolves the
	// `journalctl`/`<engine> logs` stream command (resolvePodLogsPlan), the owning plugin streams it
	// LIVE to the operator via exec.RunStream (stdio host-held). The former inline journalctl/podman
	// logs exec was DELETED — its resolution moved to the host resolver, its stream to the executor leg.
	lt, err := dispatchLifecycleTarget("logs", c.Box, c.Instance)
	if err != nil {
		return err
	}
	return lt.Logs(context.Background(), LogsOpts{Follow: c.Follow, Sidecar: c.Sidecar})
}

// podUpdateCmd is the host-side reconstruction of the former UpdateCmd (now command:update in
// candy/plugin-pod) — hostBuildPodUpdate (host_build_pod_update.go) runs its Run() body
// VERBATIM. TRACKED P13-KERNEL EXIT: dispatchByDeployTarget's resolveTreeRoot/
// loadDeployPlugins/ResolveTarget (update_deploy_dispatch.go) are core Mechanisms (the
// project loader + provider registry) a plugin cannot import or hold — this resolver moves
// through the same venue-scoped-executor-session seam when that wave lands.
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
type podUpdateCmd struct {
	Box       string
	Tag       string
	Build     bool
	Instance  string
	Seed      bool
	ForceSeed bool
	DataFrom  string
}

// Run dispatches `charly update <name>` to the target-specific update
// helper. The argument MUST resolve to a deploy entry in charly.yml
// (project + user-overlay merged). There is NO legacy fall-through to
// "treat the argument as an image name" — to refresh an image artifact
// without restarting any deploy, use `charly box pull <name>`.
//
// The dispatch keeps ZERO duplicate code paths and ZERO silent
// fallbacks. Every branch fails fast with an actionable error message.
func (c *podUpdateCmd) Run() error {
	if spec.IsRemoteImageRef(kit.StripURLScheme(c.Box)) {
		return fmt.Errorf("remote refs are not accepted here; run 'charly box pull %s' first", c.Box)
	}
	c.Box, c.Instance = deploykit.CanonicalizeDeployArg(c.Box, c.Instance)
	return c.dispatchByDeployTarget()
}

// podRemoveCmd is the host-side reconstruction of the former RemoveCmd (now command:remove in
// candy/plugin-pod) — hostBuildPodRemove (host_build_pod_remove.go) runs its Run() body VERBATIM.
// TRACKED P13-KERNEL EXIT: deeply core-type-coupled (BoxMetadata/ExtractMetadata/sidecar
// resolution/deploykit.CleanDeployEntry — not registry-bound, but not portable either), so it
// stays behind the seam alongside start/stop/logs until the P13-KERNEL wave's
// venue-scoped-executor-session seam lands.
type podRemoveCmd struct {
	Box        string
	Instance   string
	Purge      bool
	KeepDeploy bool
	Env        []string
}

func (c *podRemoveCmd) Run() error {
	c.Box, c.Instance = deploykit.CanonicalizeDeployArg(c.Box, c.Instance)
	// Releasing a persistent exclusive claim restores any holder this deploy
	// preempted (no-op if no lease / gated by an outer orchestrator).
	defer releaseResourceClaim(deploykit.DeployKey(c.Box, c.Instance))
	boxName := kit.ResolveBoxName(c.Box)

	// Stop tunnel before removing container (best-effort)
	stopTunnelForImage(boxName, c.Instance)

	rt, err := kit.ResolveRuntime()
	if err != nil {
		return err
	}

	// Resolve per-image engine from the per-host deploy config (no charly.yml dependency).
	runEngine := deploykit.ResolveBoxEngineForDeploy(boxName, c.Instance, rt.RunEngine)
	engine := kit.EngineBinary(runEngine)
	containerName := kit.ContainerNameInstance(boxName, c.Instance)

	// Run pre_remove hooks (best-effort, before stopping)
	c.runPreRemoveHook(engine, containerName, boxName)

	if rt.RunMode == "quadlet" {
		svc := kit.ServiceNameInstance(boxName, c.Instance)
		stop := exec.Command("systemctl", "--user", "stop", svc)
		_ = stop.Run()

		qdir, err := kit.QuadletDir()
		if err != nil {
			return err
		}

		qpath := filepath.Join(qdir, kit.QuadletFilenameInstance(boxName, c.Instance))
		if err := os.Remove(qpath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removing quadlet file: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Removed %s\n", qpath)

		// Remove pod file if it exists (sidecar mode)
		podPath := filepath.Join(qdir, kit.PodQuadletFilenameInstance(boxName, c.Instance))
		if err := os.Remove(podPath); err == nil {
			fmt.Fprintf(os.Stderr, "Removed %s\n", podPath)
		}

		// Remove sidecar .container files (exact-name match, no prefix
		// glob). Sources sidecar names from charly.yml — see
		// resolveSidecarNames for why charly.yml is authoritative.
		sidecarNames := resolveSidecarNames(boxName, c.Instance)
		podBase := kit.PodNameInstance(boxName, c.Instance)
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
		stopTunnel := exec.Command("systemctl", "--user", "stop", deploykit.TunnelServiceFilename(boxName))
		_ = stopTunnel.Run()
		stopEnc := exec.Command("systemctl", "--user", "stop", encServiceFilename(boxName))
		_ = stopEnc.Run()

		svcDir, svcDirErr := kit.SystemdUserDir()
		if svcDirErr == nil {
			tunnelPath := filepath.Join(svcDir, deploykit.TunnelServiceFilename(boxName))
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
			deploykit.TunnelServiceFilename(boxName),
			encServiceFilename(boxName),
		} {
			rf := exec.Command("systemctl", "--user", "reset-failed", unit)
			_ = rf.Run()
		}

		if c.Purge {
			purgeDeployArtifacts(engine, boxName, c.Instance)
		}
		if !c.KeepDeploy {
			deploykit.CleanDeployEntry(boxName, c.Instance, marshalDeployNode)
		}
		return nil
	}

	// Direct mode: stop + rm
	name := kit.ContainerNameInstance(boxName, c.Instance)

	stop := exec.Command(engine, "stop", name)
	_ = stop.Run()

	rm := exec.Command(engine, "rm", name)
	_ = rm.Run()

	fmt.Fprintf(os.Stderr, "Removed container %s\n", name)

	if c.Purge {
		purgeDeployArtifacts(engine, boxName, c.Instance)
	}
	if !c.KeepDeploy {
		deploykit.CleanDeployEntry(boxName, c.Instance, marshalDeployNode)
	}
	return nil
}

// purgeDeployArtifacts removes everything `charly remove --purge` owns for a deploy: its named
// podman volumes, its encrypted (gocryptfs) volumes, AND the synthesized <name>-overlay images an
// add_candy: overlay build produced. The overlay drop was previously reached ONLY via the pod
// substrate's PostTeardown (i.e. `charly bundle del`), so `charly remove --purge` — the teardown
// path EVERY disposable pod check bed uses (check_bed_run.go's default cleanup) — leaked its overlay
// image (dozens accumulated). Reuses kit.RemoveImagesByReference: the SAME exact-repo-matched drop
// the pod plugin's podPostTeardown uses (R3), safe against a shared-image-ID over-match.
func purgeDeployArtifacts(engine, boxName, instance string) {
	removeVolumes(engine, boxName, instance)
	removeEncryptedVolumes(boxName, instance)
	dropOverlayImagesByRef(engine, deploykit.DeployKey(boxName, instance)+"-overlay")
}

// dropOverlayImagesByRef drops the <deploy-key>-overlay images an add_candy: overlay build
// synthesized. A package var (defaulting to kit.RemoveImagesByReference) so a test can observe the
// purge WIRING — that `charly remove --purge` targets the correct `<name>-overlay` reference —
// without a live container engine.
var dropOverlayImagesByRef = kit.RemoveImagesByReference

// runPreRemoveHook runs pre_remove hooks (best-effort). Reads hooks from
// the running container's OCI labels.
func (c *podRemoveCmd) runPreRemoveHook(engine, containerName, boxName string) {
	imageRef := containerImage(engine, containerName)
	if imageRef == "" {
		return
	}
	meta, metaErr := deploykit.ExtractMetadata(engine, imageRef)
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
	out, _, exit, err := kit.RunCaptureCmd(exec.Command(kit.EngineBinary(engine), "inspect", "--format", "{{.Config.Image}}", containerName))
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

// resolveSidecarNames returns the sorted set of sidecar key names
// attached to this deploy via charly.yml. charly.yml is the
// authoritative source because sidecars only become attached via
// `charly config --sidecar <name>` which writes them into the deploy
// entry's `sidecar:` map. Image OCI labels carry sidecar TEMPLATES
// but not "which sidecars are attached to THIS deploy on THIS host".
// Returns nil when nothing is attached.
func resolveSidecarNames(boxName, instance string) []string {
	dc, err := deploykit.LoadBundleConfig()
	if err != nil || dc == nil {
		return nil
	}
	entry, ok := dc.Bundle[deploykit.DeployKey(boxName, instance)]
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
