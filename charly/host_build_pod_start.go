package main

import (
	"context"

	"github.com/opencharly/sdk/spec"
)

// host_build_pod_start.go — the "pod-start" F10 host-builder. The `charly start` command moved to
// command:start (candy/plugin-pod, the DEPLOY wave); core keeps the whole podStartCmd
// orchestration (start.go) behind this seam for now — the plugin's thin `start` command forwards
// its flags via HostBuild("pod-start") and this builder runs the existing start orchestration
// VERBATIM. Generic action noun (F11 — never a substrate word). A "stays core" claim on this
// LifecycleTarget dispatch (ResolveTarget + live-executor composition) is NOT independently
// re-verified here — see `charly bundle add`'s own deploy-dispatch cone (P13-KERNEL walk port,
// bundle_add_cmd.go / candy/plugin-bundle/walk.go), which the SAME "cannot cross the process
// boundary" reasoning once guarded and which turned out to be an incomplete seam, not permanent
// residue — before citing this file as settled precedent.
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
