package main

import (
	"context"

	"github.com/opencharly/sdk/spec"
)

// host_build_pod_logs.go — the "pod-logs" F10 host-builder. The `charly logs` command moved to
// command:logs (candy/plugin-pod, the DEPLOY wave), but the LifecycleTarget dispatch it drives —
// dispatchLifecycleTarget/ResolveTarget, the plugin loader — STAYS CORE, mirroring
// host_build_pod_start.go exactly. Core keeps the whole podLogsCmd orchestration (commands.go); the
// plugin's thin `logs` command forwards its flags via HostBuild("pod-logs") and this builder runs
// the existing logs orchestration VERBATIM. Generic action noun (F11 — never a substrate word).
const podLogsBuilderKind = "pod-logs"

func hostBuildPodLogs(_ context.Context, req spec.PodLogsRequest, _ buildEngineContext) (spec.PodLogsReply, error) {
	cmd := podLogsCmd{
		Box:      req.Box,
		Follow:   req.Follow,
		Instance: req.Instance,
		Sidecar:  req.Sidecar,
	}
	return spec.PodLogsReply{}, cmd.Run()
}

var _ = func() bool {
	registerHostBuilder(podLogsBuilderKind, typedHostBuilder(podLogsBuilderKind, hostBuildPodLogs))
	return true
}()
