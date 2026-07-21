package main

import (
	"context"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// host_build_pod_lifecycle_dispatch.go — the CONSOLIDATED "pod-{start,stop,shell,logs,update,
// service,remove}" F10 host-builder family (Cutover B unit 2, the pod-lifecycle-CLI-dispatch
// family), replacing the former host_build_pod_{start,stop,shell,logs,update,service,remove}.go
// (7 files — plus host_build_pod_disposable.go, which is a DIFFERENT concern, the AI check-harness
// disposability read, and correctly stays its own file).
//
// start/stop/shell/logs/update/service are FULLY ported: each handler wraps EXACTLY the ONE step
// that cannot cross the plugin boundary — dispatchLifecycleTarget (pod_lifecycle_verb.go):
// ResolveTarget + the plugin loader + the live-executor composition, a core (M) Mechanism per the
// kernel/plugin boundary law. Every OTHER piece of the former podStartCmd/podStopCmd/podShellCmd/
// podLogsCmd/podServiceCmd orchestration (remote-ref validation, CanonicalizeDeployArg,
// resolveServiceInit/validateServiceName/argv-rendering) MOVED to candy/plugin-pod's pod_cmd.go +
// service_resolve.go — the plugin now performs those checks itself before calling these seams,
// mirroring the P13-KERNEL deploy-node-dispatch precedent at single-node granularity (no
// ancestor-descriptor list is needed: dispatchLifecycleTarget resolves exactly ONE deploy-config
// entry, never a tree). `update` keeps its existing podUpdateCmd/dispatchByDeployTarget body
// (update_deploy_dispatch.go) UNCHANGED — that resolver is registry+loader-coupled the same way,
// just via the project tree instead of the per-host deploy config.
//
// remove is now FULLY ported too (Cutover B unit 2 completion, option (b)): candy/plugin-pod's
// RemoveCmd.Run() owns the WHOLE orchestration itself (remove_orchestration.go + remove_tunnel.go),
// reaching the host only for its two genuinely host-coupled axes via their own narrow seams
// (pod-config-hook-secret-env, pod-config-clean-deploy-entry) — see hostBuildPodRemove's own doc
// comment for why the arbiter-release bracket alone stays under this kind.
//
// Interactive/streaming safety (RDD claim, closed both at design level and on a live disposable
// bed — check-sidecar-pod, 12/12 steps PASS including the fresh `charly update` gate): this
// property is PLACEMENT-INVARIANT, not contingent on candy/plugin-pod's compiled-in-vs-out-of-
// process placement (that per-BUILD choice is never an authoring assumption). Every handler below
// is HostBuild-seam code, which by construction ALWAYS executes in the charly host process
// regardless of the CALLING plugin's placement — so isTerminal()/tty detection computed HERE sees
// the operator's REAL terminal unconditionally. The actual interactive/streaming subprocess spawn
// (`podman exec -it`/`podman logs -f`, wired to real os.Stdin/os.Stdout) is itself architecturally
// pinned to the host process too: candy/plugin-deploy-pod's podAttach/podLogs (lifecycle.go) call
// the *sdk.Executor RPC-client stub of exec.RunInteractive/RunStream, which always lands on
// charly/plugin_executor_reverse.go's executorReverseServer — living ONLY in the charly binary,
// bridged identically whether the calling plugin is compiled-in (plugin_inproc_reverse.go, a direct
// Go call) or out-of-process (sdk.ExecutorFromInvoke's go-plugin GRPCBroker dial). Proven, not
// hypothetical: candy/plugin-deploy-pod is NOT in charly.yml's compiled_plugins: today — it already
// ships out-of-process by default, and `charly shell`/`cmd`/`logs -f` already work against it in
// production on exactly this mechanism.

func hostBuildPodStart(_ context.Context, req spec.PodStartRequest, _ buildEngineContext) (spec.PodStartReply, error) {
	return spec.PodStartReply{}, startViaLifecycle(req.Box, req.Instance, podStartOpts{
		Env: req.Env, EnvFile: req.EnvFile, Port: req.Port, VolumeFlag: req.VolumeFlag,
		Bind: req.Bind, NoAutoDetect: req.NoAutoDetect,
	})
}

func hostBuildPodStop(_ context.Context, req spec.PodStopRequest, _ buildEngineContext) (spec.PodStopReply, error) {
	return spec.PodStopReply{}, stopViaLifecycle(req.Box, req.Instance, req.Unmount)
}

func hostBuildPodShell(_ context.Context, req spec.PodShellRequest, _ buildEngineContext) (spec.PodShellReply, error) {
	lt, err := dispatchLifecycleTarget("shell", req.Box, req.Instance)
	if err != nil {
		return spec.PodShellReply{}, err
	}
	opts := podShellOpts{
		Tag: req.Tag, EnvFile: req.EnvFile, Env: req.Env, VolumeFlag: req.VolumeFlag,
		Bind: req.Bind, NoAutoDetect: req.NoAutoDetect,
		// HOST-resolved NOW against the REAL terminal — see the file header invariant.
		Interactive: req.TTY || isTerminal(),
		WrapPTY:     req.TTY && !isTerminal(),
	}
	var cmd []string
	if req.Command != "" {
		cmd = []string{req.Command}
	}
	return spec.PodShellReply{}, lt.Attach(withPodShellOpts(context.Background(), opts), cmd, true)
}

func hostBuildPodLogs(_ context.Context, req spec.PodLogsRequest, _ buildEngineContext) (spec.PodLogsReply, error) {
	lt, err := dispatchLifecycleTarget("logs", req.Box, req.Instance)
	if err != nil {
		return spec.PodLogsReply{}, err
	}
	return spec.PodLogsReply{}, lt.Logs(context.Background(), LogsOpts{Follow: req.Follow, Sidecar: req.Sidecar})
}

func hostBuildPodUpdate(_ context.Context, req spec.PodUpdateRequest, _ buildEngineContext) (spec.PodUpdateReply, error) {
	cmd := podUpdateCmd{
		Box: req.Box, Tag: req.Tag, Build: req.Build, Instance: req.Instance,
		Seed: req.Seed, ForceSeed: req.ForceSeed, DataFrom: req.DataFrom,
	}
	return spec.PodUpdateReply{}, cmd.dispatchByDeployTarget()
}

// hostBuildPodService is now FULLY ported (Cutover B unit 2 completion): candy/plugin-pod's
// buildServiceArgv resolves + validates + renders the FULL argv itself (all portable — see
// service_resolve.go), so this handler does ONLY the irreducible dispatchLifecycleTarget +
// LifecycleTarget.Shell step, exactly like start/stop/logs/update above.
func hostBuildPodService(_ context.Context, req spec.PodServiceRequest, _ buildEngineContext) (spec.PodServiceReply, error) {
	lt, err := dispatchLifecycleTarget("service", req.Box, req.Instance)
	if err != nil {
		return spec.PodServiceReply{}, err
	}
	return spec.PodServiceReply{}, lt.Shell(context.Background(), req.Argv)
}

// hostBuildPodRemove is now FULLY reduced (Cutover B unit 2 remove-verb completion, option (b)):
// candy/plugin-pod's RemoveCmd.Run() owns the ENTIRE orchestration itself (remove_orchestration.go
// + remove_tunnel.go), reaching the host only for the two genuinely host-coupled axes via their own
// narrow seams (pod-config-hook-secret-env, pod-config-clean-deploy-entry). All that remains under
// THIS "pod-remove" kind is the arbiter-release bracket — CHARLY_PREEMPT_LEASE-gated host-process
// state a placement-agnostic plugin cannot own, the exact same reason pod start/stop's own arbiter
// bracket (substrate_lifecycle_grpc.go) stays core. The plugin defers this call as its LAST step,
// reproducing the former core `defer releaseResourceClaim(...)`'s "always runs, after everything
// else" semantics.
func hostBuildPodRemove(_ context.Context, req spec.PodRemoveRequest, _ buildEngineContext) (spec.PodRemoveReply, error) {
	releaseResourceClaim(deploykit.DeployKey(req.Box, req.Instance))
	return spec.PodRemoveReply{}, nil
}

var _ = func() bool {
	registerHostBuilder("pod-start", typedHostBuilder("pod-start", hostBuildPodStart))
	registerHostBuilder("pod-stop", typedHostBuilder("pod-stop", hostBuildPodStop))
	registerHostBuilder("pod-shell", typedHostBuilder("pod-shell", hostBuildPodShell))
	registerHostBuilder("pod-logs", typedHostBuilder("pod-logs", hostBuildPodLogs))
	registerHostBuilder("pod-update", typedHostBuilder("pod-update", hostBuildPodUpdate))
	registerHostBuilder("pod-service", typedHostBuilder("pod-service", hostBuildPodService))
	registerHostBuilder("pod-remove", typedHostBuilder("pod-remove", hostBuildPodRemove))
	return true
}()
