package pod

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/spec/spec"
	"gopkg.in/yaml.v3"
)

// remove_orchestration.go — the FULL `charly remove` body (Cutover B unit 2 remove-verb
// completion, option (b): full parity with the other 6 verbs). Ported VERBATIM from the former
// charly/commands.go podRemoveCmd.Run() + its runPreRemoveHook/purgeDeployArtifacts/
// resolveSidecarNames helpers, and charly/hooks.go's RunHook/removeVolumes.
//
// RDD CAUGHT A REAL LATENT BUG mid-port (not merely a test artifact — verified via a live test
// failure + full call-graph audit before accepting the "confirmed portable" framing at face
// value): sdk/deploykit.LoadFleetConfig() (and anything transitively calling it) silently
// no-ops — returns an EMPTY result, NOT an error — unless the package var deploykit.DeployStateHost
// has been populated, which happens ONLY in charly-core's OWN init() (charly/deploy_state_host.go).
// A function that "looks" portable (imports only sdk/kit + sdk/deploykit, no charly-core type)
// but transitively reaches DeployStateHost is placement-DEPENDENT: correct when compiled into the
// SAME OS process as charly-core (today's default), silently wrong (empty/ignored, no error) the
// moment it runs in a genuinely out-of-process plugin binary — exactly the class of bug the
// project's host fleet-config loader + pod-config-box-engine seams already exist to prevent
// for OTHER call sites; this port had simply not been checked against that same list yet. TWO
// functions below needed rerouting through those seams instead of calling deploykit
// directly (R3 — no new seam invented for either):
//   - resolveSidecarNames: was calling deploykit.LoadFleetConfig() raw — now goes through the
//     cycle-free loaderkit.LoadHostFleetConfigViaExecutor helper (retiring the former host
//     fleet-config loader seam).
//   - runPodRemove's engine resolution: deploykit.ResolveBoxEngineForDeploy transitively calls
//     LoadFleetConfig too (via LoadDeployConfigForRead) — now goes through the EXISTING
//     pod-config-box-engine seam (the SAME one host_build_pod_config_seams.go already serves).
//
// Every OTHER deploykit/kit call in this file was individually audited against the FULL
// DeployStateHost (and sibling "*Host" seam — none other exist) call-graph and confirmed genuinely
// self-contained (pure os/exec, path/string formatting, or self-contained label parsing): RunHook,
// removeVolumes, purgeDeployArtifacts (DeployVolumePrefix/RemoveEncryptedVolumes/
// RemoveImagesByReference), kit.ResolveRuntime, every kit naming/path helper, TunnelServiceFilename/
// EncServiceFilename, ContainerImage, ExtractMetadata, and DeployKey.
//
// The credential axis (runPreRemoveHook's secret-backed hook env) resolves PLUGIN-SIDE via
// deploykit.ResolveHookSecretEnv + this plugin's own pluginCredentialAccess (verb:credential) — the
// former pod-config-hook-secret-env seam is retired (this cone's secrets seam-death). The
// registry-resugar axis (the deploy-entry cleanup) ALSO runs PLUGIN-SIDE now: the
// pod-config-clean-deploy-entry host seam (a brief host-owns-load+lock+mutate+save stopgap — the
// plugin-side whole-config deploy-state write does not fit: it persists an already-loaded, whole,
// already-mutated FleetConfig, whereas deploykit.CleanDeployEntry loads its OWN FleetConfig under
// a file lock, does the entry-removal + provides-cleanup + empty-file-delete decision internally,
// and returns nothing) was DELETED in the #55 coneC-dsh follow-up once the loader-threaded
// Primaries marshal (podMarshalNode) + the cycle-free loaderkit reader made the same write safe
// plugin-side — see cleanDeployEntry below.
//
// The arbiter-release bracket (releaseResourceClaim, gated on the host-process
// CHARLY_PREEMPT_LEASE env var a placement-agnostic plugin cannot own) stays entirely host-side,
// under the "pod-lifecycle" HostBuild op="remove" (#55 W3 A10b unified the former dedicated
// "pod-remove" kind into this one) — same shape as pod start/stop's own arbiter
// bracket (charly/arbiter_bracket.go, S3b — was substrate_lifecycle_grpc.go before the
// deploy-dispatch cluster moved). RemoveCmd.Run() (pod_cmd.go) defers a call to it as the
// LAST step, mirroring the former `defer releaseResourceClaim(...)` at the top of podRemoveCmd.Run()
// (a defer runs at function-return time regardless of path, so "call it last" here reproduces the
// exact same "always runs, after everything else" semantics).

// credServiceVNC mirrors charly/credential_plugin.go's CredServiceVNC (the VNC credential service
// name deploykit's secret helpers key the auto-generated VNC password under).
const credServiceVNC = "charly/vnc"

// runHook executes a hook script inside a running container (relocated from charly/hooks.go's
// RunHook — zero core-registry coupling, its only caller was podRemoveCmd).
func runHook(engine, containerName, hookScript string, envVars []string) error {
	if hookScript == "" {
		return nil
	}
	args := []string{"exec"}
	args = append(args, "-e", "CHARLY_CONTAINER_NAME="+containerName)
	for _, env := range envVars {
		args = append(args, "-e", env)
	}
	args = append(args, containerName, "sh", "-c", hookScript)

	cmd := exec.Command(engine, args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr

	fmt.Fprintf(os.Stderr, "Running hook in %s...\n", containerName)
	return cmd.Run()
}

// removeVolumes removes all named volumes matching the image/instance prefix (relocated from
// charly/hooks.go — zero core-registry coupling, its only caller was podRemoveCmd).
func removeVolumes(engine, boxName, instance string) {
	prefix := deploykit.DeployVolumePrefix(boxName, instance)

	out, err := exec.Command(engine, "volume", "ls", "--format", "{{.Name}}", "--filter", "name="+prefix).Output()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: listing volumes: %v\n", err)
		return
	}
	for name := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if name == "" {
			continue
		}
		rm := exec.Command(engine, "volume", "rm", name)
		rm.Stderr = os.Stderr
		if err := rm.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: removing volume %s: %v\n", name, err)
		} else {
			fmt.Fprintf(os.Stderr, "Removed volume %s\n", name)
		}
	}
}

// dropOverlayImagesByRef drops the <deploy-key>-overlay images an add_candy: overlay build
// synthesized. A package var (defaulting to kit.RemoveImagesByReference, same as
// candy/plugin-deploy-pod's own podPostTeardown already calls directly, R3) so a test can observe
// the purge WIRING — that `charly remove --purge` targets the correct `<name>-overlay` reference —
// without a live container engine (relocated from charly/commands.go, same test-observability
// pattern preserved).
var dropOverlayImagesByRef = kit.RemoveImagesByReference

// purgeDeployArtifacts removes everything `charly remove --purge` owns for a deploy: its named
// podman volumes, its encrypted (gocryptfs) volumes, AND the synthesized <name>-overlay images an
// add_candy: overlay build produced (relocated from charly/commands.go — zero core-registry
// coupling).
// sidecarContainerNames maps a pod's sidecar keys to the container names their quadlets produce.
// Pure, so the naming convention stays testable without touching a container engine.
func sidecarContainerNames(podBase string, sidecarNames []string) []string {
	out := make([]string, 0, len(sidecarNames))
	for _, sc := range sidecarNames {
		out = append(out, podBase+"-"+sc)
	}
	return out
}

// containerExists reports whether the engine still knows a container by that name. R3
// consolidation (K-wave 2 cone R2 bank C): delegates to the shared kit.ContainerExists; the var
// stays so the removal ordering can still be tested without a live engine. `engine` here is the
// resolved binary path (kit.EngineBinary output — "podman"/"docker"), idempotent through
// kit.ContainerExists's own EngineBinary resolve.
var containerExists = kit.ContainerExists

// removeContainer force-removes one container. Package var for the same reason as above.
var removeContainer = func(engine, name string) error {
	return exec.Command(engine, "rm", "-f", name).Run()
}

// ensureContainersRemoved reaps any container that outlived its systemd unit. See the call site
// for why an unreaped container becomes a permanent orphan once its quadlet file is deleted.
func ensureContainersRemoved(engine string, names []string) {
	for _, name := range names {
		if name == "" || !containerExists(engine, name) {
			continue
		}
		fmt.Fprintf(os.Stderr, "Container %s outlived its unit; removing it directly\n", name)
		if err := removeContainer(engine, name); err != nil {
			// Warn rather than fail: teardown must continue so the remaining artifacts still get
			// cleaned up, but the operator needs to know a container was left behind.
			fmt.Fprintf(os.Stderr, "Warning: removing leftover container %s: %v\n", name, err)
		}
	}
}

func purgeDeployArtifacts(engine, boxName, instance string) {
	removeVolumes(engine, boxName, instance)
	deploykit.RemoveEncryptedVolumes(boxName, instance)
	dropOverlayImagesByRef(engine, spec.DeployKey(boxName, instance)+"-overlay")
}

// resolveSidecarNames returns the sorted set of sidecar key names attached to this deploy via
// charly.yml. Relocated from charly/commands.go — the per-host overlay read is now
// loaderkit.LoadHostFleetConfigViaExecutor (#55 coneC Unit C2: the cycle-free plugin-side helper
// that replaced the deleted deploykit.LoadFleetConfigViaSeam host-seam
// round-trip). The bare deploykit.LoadFleetConfig silently no-ops unless
// deploykit.DeployStateHost is set (charly-core's own init() only), so a plugin calling it
// directly would silently see no sidecars to clean up whenever NOT compiled into the charly-core
// process — the loaderkit helper drives LoadUnified over the reverse channel instead. R3 hoist
// (charly#176 round 1): the former LoadFleetConfigViaSeam itself hoisted four near-identical
// local copies (candy/plugin-fleet/ephemeral.go, candy/plugin-status/nested_tree.go,
// candy/plugin-substrate/status_flat.go, this one); the C2 helper is now the ONE shared
// implementation all four call. Kept as a thin wrapper (untested at unit level, same as every
// other overlay read — proved live by the disposable bed) around sidecarNamesFromFleetConfig,
// the pure extraction logic the ORIGINAL unit test actually exercised, kept independently
// testable without a reverse channel.
func resolveSidecarNames(boxName, instance string) []string {
	dc, err := loaderkit.LoadHostFleetConfigViaExecutor(cmdCtx, cmdExec)
	if err != nil || dc == nil {
		return nil
	}
	return sidecarNamesFromFleetConfig(dc, boxName, instance)
}

// sidecarNamesFromFleetConfig is the pure extraction logic pulled out of resolveSidecarNames so
// it stays unit-testable without a live reverse channel (see resolveSidecarNames' doc comment).
func sidecarNamesFromFleetConfig(dc *deploykit.FleetConfig, boxName, instance string) []string {
	if dc == nil {
		return nil
	}
	entry, ok := dc.Fleet[spec.DeployKey(boxName, instance)]
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

// runPreRemoveHook runs pre_remove hooks (best-effort). Reads hooks from the running container's
// OCI labels; the credential-backed hook env is resolved PLUGIN-SIDE via deploykit.ResolveHookSecretEnv
// + this plugin's own pluginCredentialAccess (verb:credential — the SAME drive the enc/start path
// uses), no host seam (the former pod-config-hook-secret-env seam is retired, this cone's secrets
// seam-death).
func runPreRemoveHook(engine, containerName, boxName, instance string, cliEnv []string) {
	imageRef := kit.ContainerImage(engine, containerName)
	if imageRef == "" {
		return
	}
	meta, metaErr := deploykit.ExtractMetadata(engine, imageRef)
	if metaErr != nil || meta == nil || meta.Hook == nil || meta.Hook.PreRemove == "" {
		return
	}
	secretEnv := deploykit.ResolveHookSecretEnv(boxName, instance, meta, credServiceVNC, pluginCredentialAccess())
	hookEnv := append(append([]string{}, cliEnv...), secretEnv...)
	if err := runHook(engine, containerName, meta.Hook.PreRemove, hookEnv); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: pre_remove hook failed: %v\n", err)
	}
}

// podMarshalNode builds the per-entry node-form marshal callback deploykit.CleanDeployEntry takes.
// It resugars each plan step via the loader-threaded Primaries snapshot (HostBuild("loader-threaded")
// — the SAME permanent D-fact leg candy/plugin-fleet/plugin-deploy-pod use), so the clean runs
// PLUGIN-SIDE without the charly-init DeployStateHost registration (#55 coneC-dsh — the
// pod-config-clean-deploy-entry host seam is DELETED). A HostBuild failure degrades to an empty
// map (a plan with no plugin-verb sugar marshals identically).
func podMarshalNode() func(name string, node *deploykit.FleetNode) (*yaml.Node, error) {
	primaries := map[string]string{}
	if cmdExec != nil {
		if out, err := cmdExec.HostBuild(cmdCtx, "loader-threaded", nil); err == nil {
			var t spec.Threaded
			if json.Unmarshal(out, &t) == nil {
				primaries = t.Primaries
			}
		}
	}
	return func(_ string, node *deploykit.FleetNode) (*yaml.Node, error) {
		return deploykit.MarshalFleetNode(node, primaries)
	}
}

// cleanDeployEntry runs deploykit.CleanDeployEntry PLUGIN-SIDE (#55 coneC-dsh — the
// pod-config-clean-deploy-entry host seam is DELETED). The reader is the cycle-free
// loaderkit.LoadHostFleetConfigViaExecutor read (loadPodFleetConfig); the marshal resugars via
// loader-threaded Primaries (podMarshalNode). Best-effort (deploykit.CleanDeployEntry swallows its
// own errors with stderr warnings) — matching the former host leg.
func cleanDeployEntry(boxName, instance string) error {
	deploykit.CleanDeployEntry(boxName, instance, podMarshalNode(), loadPodFleetConfig)
	return nil
}

// runPodRemove is the full `charly remove` orchestration, ported verbatim from the former
// charly/commands.go podRemoveCmd.Run() (minus tunnel-stop, already handled by the caller, and
// minus the arbiter-release bracket, which the caller defers to the host seam).
func runPodRemove(box, instance string, purge, keepDeploy bool, cliEnv []string) error {
	boxName := kit.ResolveBoxName(box)

	rt, err := kit.ResolveRuntime()
	if err != nil {
		return err
	}

	// Resolve per-image engine from the per-host deploy config PLUGIN-SIDE (loadPodFleetConfig —
	// the cycle-free loaderkit read), replicating deploykit.ResolveBoxEngineForDeploy's lookup
	// WITHOUT the DeployStateHost package-var dependency (a plugin calling the raw deploykit
	// function would silently no-op out-of-process). #55 coneC-dsh — the pod-config-box-engine host
	// seam is DELETED.
	runEngine := rt.RunEngine
	if dc, _ := loadPodFleetConfig(); dc != nil {
		if entry, ok := dc.Lookup(boxName, instance); ok && entry.Engine != "" {
			runEngine = entry.Engine
		}
	}
	engine := kit.EngineBinary(runEngine)
	containerName := kit.ContainerNameInstance(boxName, instance)

	// Run pre_remove hooks (best-effort, before stopping)
	runPreRemoveHook(engine, containerName, boxName, instance, cliEnv)

	if rt.RunMode == "quadlet" {
		svc := kit.ServiceNameInstance(boxName, instance)
		// Sidecar names are needed BEFORE any file is deleted: their services have to be stopped
		// while their units still exist, and their containers reaped before the quadlets go away.
		sidecarNames := resolveSidecarNames(boxName, instance)
		podBase := kit.PodNameInstance(boxName, instance)

		// Stop the sidecars first, then the main service. A sidecar's unit is generated from its
		// .container file, so stopping it after that file is deleted (and the daemon reloaded) is
		// no longer possible — its container would simply be left running.
		for _, sc := range sidecarNames {
			_ = exec.Command("systemctl", "--user", "stop", podBase+"-"+sc+".service").Run()
		}
		stop := exec.Command("systemctl", "--user", "stop", svc)
		_ = stop.Run()

		// Confirm the containers are actually GONE before the quadlet files are deleted.
		//
		// The systemctl stop above is best-effort by design, but its outcome is load-bearing: it
		// is the ONLY thing that reaps the container in quadlet mode (unlike the direct-mode
		// branch below, which explicitly stops and removes). If it does not — a unit stuck in
		// failed/activating, an ExecStop that hits its stop timeout, a unit systemd no longer
		// knows about — the container survives; and once the quadlet file is deleted and the
		// daemon reloaded, the unit ceases to exist and NOTHING will ever tear that container
		// down again. It is then orphaned permanently, holding its netns and, with it, its
		// aardvark-dns registration — which is how a stale DNS entry outlives the deploy that
		// created it and breaks container-name resolution host-wide.
		//
		// Verify-then-remove, so a container systemd already reaped costs one cheap existence
		// check and nothing else.
		names := append([]string{containerName}, sidecarContainerNames(podBase, sidecarNames)...)
		ensureContainersRemoved(engine, names)

		qdir, err := kit.QuadletDir()
		if err != nil {
			return err
		}

		qpath := filepath.Join(qdir, kit.QuadletFilenameInstance(boxName, instance))
		if err := os.Remove(qpath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removing quadlet file: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Removed %s\n", qpath)

		// Remove pod file if it exists (sidecar mode)
		podPath := filepath.Join(qdir, kit.PodQuadletFilenameInstance(boxName, instance))
		if err := os.Remove(podPath); err == nil {
			fmt.Fprintf(os.Stderr, "Removed %s\n", podPath)
		}

		// Remove sidecar .container files (exact-name match, no prefix glob). Sources sidecar
		// names from charly.yml — see resolveSidecarNames for why charly.yml is authoritative.
		for _, sc := range sidecarNames {
			scPath := filepath.Join(qdir, podBase+"-"+sc+".container")
			if err := os.Remove(scPath); err == nil {
				fmt.Fprintf(os.Stderr, "Removed %s\n", scPath)
			}
		}

		// Remove sidecar config files. Naming convention is `<podBase>-<sidecar>-<purpose>.<ext>`
		// (e.g. charly-foo-tailscale-serve.json). The prefix is anchored to the sidecar NAME so
		// unrelated sidecars/bases can't match.
		if scDir, scErr := kit.SidecarConfigDir(); scErr == nil {
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
		stopEnc := exec.Command("systemctl", "--user", "stop", deploykit.EncServiceFilename(boxName))
		_ = stopEnc.Run()

		svcDir, svcDirErr := kit.SystemdUserDir()
		if svcDirErr == nil {
			tunnelPath := filepath.Join(svcDir, deploykit.TunnelServiceFilename(boxName))
			if err := os.Remove(tunnelPath); err == nil {
				fmt.Fprintf(os.Stderr, "Removed %s\n", tunnelPath)
			}
			encPath := filepath.Join(svcDir, deploykit.EncServiceFilename(boxName))
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
			deploykit.EncServiceFilename(boxName),
		} {
			rf := exec.Command("systemctl", "--user", "reset-failed", unit)
			_ = rf.Run()
		}

		if purge {
			purgeDeployArtifacts(engine, boxName, instance)
		}
		if !keepDeploy {
			if err := cleanDeployEntry(boxName, instance); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: cleaning deploy entry: %v\n", err)
			}
		}
		return nil
	}

	// Direct mode: stop + rm
	name := kit.ContainerNameInstance(boxName, instance)

	stop := exec.Command(engine, "stop", name)
	_ = stop.Run()

	rm := exec.Command(engine, "rm", name)
	_ = rm.Run()

	fmt.Fprintf(os.Stderr, "Removed container %s\n", name)

	if purge {
		purgeDeployArtifacts(engine, boxName, instance)
	}
	if !keepDeploy {
		if err := cleanDeployEntry(boxName, instance); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: cleaning deploy entry: %v\n", err)
		}
	}
	return nil
}
