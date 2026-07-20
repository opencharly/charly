package main

import (
	"context"

	"github.com/opencharly/sdk/spec"
)

// host_build_pod_shell.go — the "pod-shell" F10 host-builder. The `charly shell` command moved
// to command:shell (candy/plugin-pod, the DEPLOY wave), but the LifecycleTarget dispatch it
// drives — dispatchLifecycleTarget/ResolveTarget, the plugin loader — STAYS CORE, mirroring
// host_build_pod_start.go exactly. Core keeps the whole podShellCmd orchestration (shell.go); the
// plugin's thin `shell` command forwards its flags via HostBuild("pod-shell") and this builder
// runs the existing shell orchestration VERBATIM. Generic action noun (F11 — never a substrate word).
const podShellBuilderKind = "pod-shell"

func hostBuildPodShell(_ context.Context, req spec.PodShellRequest, _ buildEngineContext) (spec.PodShellReply, error) {
	cmd := podShellCmd{
		Box:          req.Box,
		Tag:          req.Tag,
		Command:      req.Command,
		Build:        req.Build,
		TTY:          req.TTY,
		Env:          req.Env,
		EnvFile:      req.EnvFile,
		Instance:     req.Instance,
		VolumeFlag:   req.VolumeFlag,
		Bind:         req.Bind,
		NoAutoDetect: req.NoAutoDetect,
	}
	return spec.PodShellReply{}, cmd.Run()
}

var _ = func() bool {
	registerHostBuilder(podShellBuilderKind, typedHostBuilder(podShellBuilderKind, hostBuildPodShell))
	return true
}()
