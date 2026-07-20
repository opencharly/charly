package main

import (
	"context"

	"github.com/opencharly/sdk/spec"
)

// host_build_pod_stop.go — the "pod-stop" F10 host-builder. The `charly stop` command moved to
// command:stop (candy/plugin-pod, the DEPLOY wave), but the LifecycleTarget dispatch it drives —
// ResolveTarget, the plugin loader, and the live-executor composition — STAYS CORE, mirroring
// host_build_pod_start.go exactly. Core keeps the whole podStopCmd orchestration (start.go); the
// plugin's thin `stop` command forwards its flags via HostBuild("pod-stop") and this builder runs
// the existing stop orchestration VERBATIM. Generic action noun (F11 — never a substrate word).
const podStopBuilderKind = "pod-stop"

func hostBuildPodStop(_ context.Context, req spec.PodStopRequest, _ buildEngineContext) (spec.PodStopReply, error) {
	cmd := podStopCmd{
		Box:      req.Box,
		Instance: req.Instance,
		Unmount:  req.Unmount,
	}
	return spec.PodStopReply{}, cmd.Run()
}

var _ = func() bool {
	registerHostBuilder(podStopBuilderKind, typedHostBuilder(podStopBuilderKind, hostBuildPodStop))
	return true
}()
