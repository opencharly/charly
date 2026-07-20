package main

import (
	"context"

	"github.com/opencharly/sdk/spec"
)

// host_build_pod_start.go — the "pod-start" F10 host-builder. The `charly start` command moved to
// command:start (candy/plugin-pod, the DEPLOY wave), but the LifecycleTarget dispatch it drives —
// ResolveTarget, the plugin loader, and the live-executor composition (host objects that cannot
// cross the process boundary) — STAYS CORE, exactly as the deploy kernel stayed core behind
// HostBuild("deploy-add") in P13. So core keeps the whole podStartCmd orchestration (start.go); the
// plugin's thin `start` command forwards its flags via HostBuild("pod-start") and this builder runs
// the existing start orchestration VERBATIM. Generic action noun (F11 — never a substrate word).
const podStartBuilderKind = "pod-start"

func hostBuildPodStart(_ context.Context, req spec.PodStartRequest, _ buildEngineContext) (spec.PodStartReply, error) {
	cmd := podStartCmd{
		Box:          req.Box,
		Tag:          req.Tag,
		Build:        req.Build,
		Env:          req.Env,
		EnvFile:      req.EnvFile,
		Instance:     req.Instance,
		Port:         req.Port,
		VolumeFlag:   req.VolumeFlag,
		Bind:         req.Bind,
		NoAutoDetect: req.NoAutoDetect,
	}
	return spec.PodStartReply{}, cmd.Run()
}

var _ = func() bool {
	registerHostBuilder(podStartBuilderKind, typedHostBuilder(podStartBuilderKind, hostBuildPodStart))
	return true
}()
