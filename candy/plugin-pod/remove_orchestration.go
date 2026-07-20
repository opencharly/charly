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
	"github.com/opencharly/sdk/spec"
)

// remove_orchestration.go — the FULL `charly remove` body (Cutover B unit 2 remove-verb
// completion, option (b): full parity with the other 6 verbs). Ported VERBATIM from the former
// charly/commands.go podRemoveCmd.Run() + its runPreRemoveHook/purgeDeployArtifacts/
// resolveSidecarNames helpers, and charly/hooks.go's RunHook/removeVolumes.
//
// RDD CAUGHT A REAL LATENT BUG mid-port (not merely a test artifact — verified via a live test
// failure + full call-graph audit before accepting the "confirmed portable" framing at face
// value): sdk/deploykit.LoadBundleConfig() (and anything transitively calling it) silently
// no-ops — returns an EMPTY result, NOT an error — unless the package var deploykit.DeployStateHost
// has been populated, which happens ONLY in charly-core's OWN init() (charly/deploy_state_host.go).
// A function that "looks" portable (imports only sdk/kit + sdk/deploykit, no charly-core type)
// but transitively reaches DeployStateHost is placement-DEPENDENT: correct when compiled into the
// SAME OS process as charly-core (today's default), silently wrong (empty/ignored, no error) the
// moment it runs in a genuinely out-of-process plugin binary — exactly the class of bug the
// project's existing pod-config-load-bundle/pod-config-box-engine seams already exist to prevent
// for OTHER call sites; this port had simply not been checked against that same list yet. TWO
// functions below needed rerouting through their EXISTING seams instead of calling deploykit
// directly (R3 — no new seam invented for either):
//   - resolveSidecarNames: was calling deploykit.LoadBundleConfig() raw — now goes through the
//     EXISTING pod-config-load-bundle seam.
//   - runPodRemove's engine resolution: deploykit.ResolveBoxEngineForDeploy transitively calls
//     LoadBundleConfig too (via LoadDeployConfigForRead) — now goes through the EXISTING
//     pod-config-box-engine seam (the SAME one host_build_pod_config_seams.go already serves).
//
// Every OTHER deploykit/kit call in this file was individually audited against the FULL
// DeployStateHost (and sibling "*Host" seam — none other exist) call-graph and confirmed genuinely
// self-contained (pure os/exec, path/string formatting, or self-contained label parsing): RunHook,
// removeVolumes, purgeDeployArtifacts (DeployVolumePrefix/RemoveEncryptedVolumes/
// RemoveImagesByReference), kit.ResolveRuntime, every kit naming/path helper, TunnelServiceFilename/
// EncServiceFilename, ContainerImage, ExtractMetadata, and DeployKey.
//
// Two further axes remain genuinely host-coupled by DESIGN (not this same DeployStateHost class)
// and reach the host over their own EXISTING narrow seams (R3 — no new seam invented for either):
//   - the credential axis (runPreRemoveHook's secret-backed hook env) reuses
//     pod-config-hook-secret-env, the SAME seam pod-config-setup already calls;
//   - the registry-resugar axis (the deploy-entry cleanup) needs a NEW narrow twin,
//     pod-config-clean-deploy-entry, mirroring pod-config-save-deploy-state's shape exactly —
//     the EXISTING deploy-config-save seam does NOT fit: it persists an already-loaded, whole,
//     already-mutated BundleConfig (bundle import/reset's use case, no internal load, no lock, no
//     entry-removal/provides-cleanup logic), whereas deploykit.CleanDeployEntry loads its OWN
//     BundleConfig under a file lock, does the entry-removal + provides-cleanup + empty-file-delete
//     decision internally, and returns nothing — a fundamentally different, narrower operation.
//     Forking a twin here (rather than bending the wrong-shaped seam to fit) was cleared with
//     team-lead after demonstrating this exact mismatch.
//
// The arbiter-release bracket (releaseResourceClaim, gated on the host-process
// CHARLY_PREEMPT_LEASE env var a placement-agnostic plugin cannot own) stays entirely host-side,
// under the EXISTING "pod-remove" HostBuild kind — same shape as pod start/stop's own arbiter
// bracket (substrate_lifecycle_grpc.go). RemoveCmd.Run() (pod_cmd.go) defers a call to it as the
// LAST step, mirroring the former `defer releaseResourceClaim(...)` at the top of podRemoveCmd.Run()
// (a defer runs at function-return time regardless of path, so "call it last" here reproduces the
// exact same "always runs, after everything else" semantics).

// podConfigHookSecretEnvKind / podConfigCleanDeployEntryKind / podConfigLoadBundleKind /
// podConfigBoxEngineKind are wire kind strings for charly/host_build_pod_config_seams.go's
// hostBuildPodConfigHookSecretEnv, hostBuildPodConfigCleanDeployEntry (NEW, this cutover),
// hostBuildPodConfigLoadBundle, and hostBuildPodConfigBoxEngine (all four EXISTING except
// CleanDeployEntry, reused as-is) — plain protocol literals (R3: kind names are wire strings, not
// shared Go symbols, so each consuming module names its own const, same convention as
// podConfigContainerTunnelKind in remove_tunnel.go).
const (
	podConfigHookSecretEnvKind    = "pod-config-hook-secret-env"
	podConfigCleanDeployEntryKind = "pod-config-clean-deploy-entry"
	podConfigLoadBundleKind       = "pod-config-load-bundle"
	podConfigBoxEngineKind        = "pod-config-box-engine"
)

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
func purgeDeployArtifacts(engine, boxName, instance string) {
	removeVolumes(engine, boxName, instance)
	deploykit.RemoveEncryptedVolumes(boxName, instance)
	dropOverlayImagesByRef(engine, deploykit.DeployKey(boxName, instance)+"-overlay")
}

// resolveSidecarNames returns the sorted set of sidecar key names attached to this deploy via
// charly.yml. Relocated from charly/commands.go — its raw deploykit.LoadBundleConfig() call is
// REROUTED through the EXISTING pod-config-load-bundle seam (an RDD-caught fix, see this file's
// header): LoadBundleConfig silently no-ops unless deploykit.DeployStateHost is set, which happens
// ONLY in charly-core's own init(), so a plugin calling it directly would silently see no sidecars
// to clean up whenever NOT compiled into the charly-core process. Split into a thin seam-calling
// wrapper (untested at unit level, same as every other pod-config-* seam call — proved live by the
// disposable bed) and sidecarNamesFromBundleConfig, the pure extraction logic the ORIGINAL unit
// test actually exercised, kept independently testable without a reverse channel.
func resolveSidecarNames(boxName, instance string) []string {
	var rep spec.PodConfigLoadBundleReply
	if err := hostPodSeamReply(podConfigLoadBundleKind, spec.PodConfigLoadDeployRequest{Caller: "charly remove sidecar sweep"}, &rep); err != nil || len(rep.ConfigJSON) == 0 {
		return nil
	}
	var dc deploykit.BundleConfig
	if json.Unmarshal(rep.ConfigJSON, &dc) != nil {
		return nil
	}
	return sidecarNamesFromBundleConfig(&dc, boxName, instance)
}

// sidecarNamesFromBundleConfig is the pure extraction logic pulled out of resolveSidecarNames so
// it stays unit-testable without a live reverse channel (see resolveSidecarNames' doc comment).
func sidecarNamesFromBundleConfig(dc *deploykit.BundleConfig, boxName, instance string) []string {
	if dc == nil {
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

// runPreRemoveHook runs pre_remove hooks (best-effort). Reads hooks from the running container's
// OCI labels; the credential-backed hook env is resolved via the EXISTING pod-config-hook-secret-env
// seam (the SAME one pod-config-setup calls) instead of a local resolveHookSecretEnv — that
// function is registry/credential-coupled and stays host-side.
func runPreRemoveHook(engine, containerName, boxName, instance string, cliEnv []string) {
	imageRef := kit.ContainerImage(engine, containerName)
	if imageRef == "" {
		return
	}
	meta, metaErr := deploykit.ExtractMetadata(engine, imageRef)
	if metaErr != nil || meta == nil || meta.Hook == nil || meta.Hook.PreRemove == "" {
		return
	}
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: marshaling metadata for pre_remove hook env: %v\n", err)
		return
	}
	var secretRep spec.PodConfigHookSecretEnvReply
	var secretEnv []string
	if err := hostPodSeamReply(podConfigHookSecretEnvKind, spec.PodConfigHookSecretEnvRequest{
		Box: boxName, Instance: instance, MetaJSON: metaJSON,
	}, &secretRep); err == nil {
		secretEnv = secretRep.Env
	}
	hookEnv := append(append([]string{}, cliEnv...), secretEnv...)
	if err := runHook(engine, containerName, meta.Hook.PreRemove, hookEnv); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: pre_remove hook failed: %v\n", err)
	}
}

// cleanDeployEntry asks the host to run deploykit.CleanDeployEntry(box, instance, marshalDeployNode)
// via the NEW narrow pod-config-clean-deploy-entry seam (see the file header for why the EXISTING
// deploy-config-save seam does not fit).
func cleanDeployEntry(boxName, instance string) error {
	return hostPodSeam(podConfigCleanDeployEntryKind, spec.PodConfigCleanDeployEntryRequest{Box: boxName, Instance: instance})
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

	// Resolve per-image engine from the per-host deploy config (no charly.yml dependency).
	// Rerouted through the EXISTING pod-config-box-engine seam (RDD-caught fix, see this file's
	// header): deploykit.ResolveBoxEngineForDeploy transitively calls LoadBundleConfig too, which
	// silently no-ops outside the charly-core process.
	var boxEngineRep spec.PodConfigBoxEngineReply
	if err := hostPodSeamReply(podConfigBoxEngineKind, spec.PodConfigBoxEngineRequest{
		Box: boxName, Instance: instance, GlobalEngine: rt.RunEngine,
	}, &boxEngineRep); err != nil {
		return err
	}
	runEngine := boxEngineRep.Engine
	engine := kit.EngineBinary(runEngine)
	containerName := kit.ContainerNameInstance(boxName, instance)

	// Run pre_remove hooks (best-effort, before stopping)
	runPreRemoveHook(engine, containerName, boxName, instance, cliEnv)

	if rt.RunMode == "quadlet" {
		svc := kit.ServiceNameInstance(boxName, instance)
		stop := exec.Command("systemctl", "--user", "stop", svc)
		_ = stop.Run()

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
		sidecarNames := resolveSidecarNames(boxName, instance)
		podBase := kit.PodNameInstance(boxName, instance)
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
