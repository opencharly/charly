package main

import (
	"os"
	"os/exec"

	"github.com/opencharly/sdk/kit"
)

// isTerminal reports whether stdout is connected to a terminal. Package-level var for testability.
// Relocated from the deleted shell.go (Cutover B unit 2) — used by host_build_pod_lifecycle_dispatch.go's
// hostBuildPodShell (the TTY-detection invariant documented there).
var isTerminal = defaultIsTerminal

func defaultIsTerminal() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

// containerRunning/defaultContainerRunning DELETED (Cutover B unit 2, R1 divergence caught mid-flight):
// this was a duplicate of the ALREADY-EXISTING sdk/kit.ContainerRunning (kit/container_probe.go,
// relocated from this exact file's predecessor charly/shell.go per its OWN header comment, which
// claimed callers "now import kit directly" — a false claim until this fix: android_deploy_cmd.go
// and service.go were still calling the LOCAL var). Every caller now calls kit.ContainerRunning.

// containerExists reports whether a container with the given name is present in the engine's
// storage, RUNNING OR STOPPED (unlike containerRunning, which is false for a stopped container). A
// bare `container inspect` succeeds for any existing container, so its exit status is the signal.
// Relocated from the deleted shell.go (Cutover B unit 2) — still used by bundle_add_cmd.go.
var containerExists = func(engine, name string) bool {
	binary := kit.EngineBinary(engine)
	return exec.Command(binary, "container", "inspect", name).Run() == nil
}

// stopTunnelForImage DELETED (Cutover B unit 2 remove-verb completion) — its only caller,
// podRemoveCmd.Run() below, now has the tunnel already torn down BEFORE this seam runs:
// candy/plugin-pod's RemoveCmd.Run() resolves the container's tunnel config via the EXISTING
// pod-config-container-tunnel seam and stops it itself via verb:tunnel over InvokeProvider
// (remove_tunnel.go), the same mechanism candy/plugin-deploy-pod's start/stop already use. No
// remaining core caller dispatches a tunnel stop for the remove path — the former core
// tunnel-dispatch adapter (tunnel.go's sibling file, now deleted) has zero callers left.

// podUpdateCmd is the host-side dispatch struct for `charly update` (now command:update in
// candy/plugin-pod). Cutover B unit 2: the plugin now performs the remote-ref/CanonicalizeDeployArg
// validation itself (candy/plugin-pod's UpdateCmd.Run()) before reaching HostBuild("pod-update")
// (host_build_pod_lifecycle_dispatch.go), which constructs this struct directly and calls
// dispatchByDeployTarget() — no more Run()-VERBATIM reconstruction. dispatchByDeployTarget's
// resolveTreeRoot/loadDeployPlugins/ResolveTarget (update_deploy_dispatch.go) remain core
// Mechanisms (the project loader + provider registry) a plugin cannot import or hold.
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

// podRemoveCmd (+ purgeDeployArtifacts, dropOverlayImagesByRef, runPreRemoveHook,
// resolveSidecarNames) DELETED (Cutover B unit 2 remove-verb completion, option (b) — full parity
// with the other 6 verbs): the WHOLE orchestration is now candy/plugin-pod's
// (remove_orchestration.go's runPodRemove, RemoveCmd.Run() in pod_cmd.go), confirmed portable
// (pure os/exec + sdk/kit + sdk/deploykit, zero core-registry coupling — each of these functions
// had EXACTLY ONE caller, this type). The two genuinely host-coupled axes (the credential-backed
// hook env; the deploy-entry cleanup's registry-resugar) reach the host over their own narrow
// seams (pod-config-hook-secret-env, the NEW pod-config-clean-deploy-entry —
// host_build_pod_config_seams.go). The arbiter-release bracket alone remains under
// hostBuildPodRemove ("pod-remove", host_build_pod_lifecycle_dispatch.go).

// containerImageRef/containerImage DELETED (Cutover B unit 2, R1 divergence caught mid-flight):
// both were duplicates of the ALREADY-EXISTING sdk/kit.ContainerImageRef/kit.ContainerImage
// (kit/container_image.go, relocated from THIS exact file per its own header comment, which
// claimed callers "now import kit directly" — a false claim until this fix: commands.go itself,
// check_endpoint_resolve.go, service.go, and pod_lifecycle_resolve.go were still calling the
// LOCAL functions). Every caller now calls kit.ContainerImageRef/kit.ContainerImage.
