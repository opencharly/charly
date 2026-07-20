package main

import (
	"context"

	"github.com/opencharly/sdk/spec"
)

// host_build_pod_service.go — the "pod-service" F10 host-builder. The `charly service …` command
// moved to command:service (candy/plugin-pod, the DEPLOY wave), but the LifecycleTarget dispatch
// it drives — dispatchLifecycleTarget/ResolveTarget, the plugin loader — STAYS CORE, mirroring
// host_build_pod_start.go exactly. Core keeps the whole podServiceCmd orchestration (service.go);
// the plugin's thin `service start/stop/status/restart` leaves forward via
// HostBuild("pod-service") (one seam, an Operation discriminator) and this builder runs the
// existing service orchestration VERBATIM. Generic action noun (F11 — never a substrate word).
const podServiceBuilderKind = "pod-service"

func hostBuildPodService(_ context.Context, req spec.PodServiceRequest, _ buildEngineContext) (spec.PodServiceReply, error) {
	cmd := podServiceCmd{
		Operation: req.Operation,
		Box:       req.Box,
		Service:   req.Service,
		Instance:  req.Instance,
	}
	return spec.PodServiceReply{}, cmd.Run()
}

var _ = func() bool {
	registerHostBuilder(podServiceBuilderKind, typedHostBuilder(podServiceBuilderKind, hostBuildPodService))
	return true
}()
