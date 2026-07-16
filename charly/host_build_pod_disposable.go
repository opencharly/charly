package main

import (
	"context"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// host_build_pod_disposable.go — the THIN "pod-disposable" host seam (K5-U2/3). It resolves the ONE
// AI-harness check-project fact the resolved-project envelope cannot carry: whether a per-host POD
// deploy overlay entry is disposable. The harness's iterate sandbox is an operator-provisioned
// per-host deploy (`charly bundle add <sandbox> <ref> --disposable`), so its disposability lives in
// the per-host overlay (LoadBundleConfig → ~/.config/charly/charly.yml), NOT the project charly.yml
// the resolved-project envelope projects (Mode Purity keeps the overlay out of the build-mode
// projection). The overlay read needs the core loader a plugin cannot import, and no deploy/status
// provider serves it, so plugin-check reaches it over this seam. The harness fresh-per-run restart
// gate reads Disposable. Class-generic action noun "pod-disposable" (F11 — never a substrate word).
const podDisposableBuilderKind = "pod-disposable"

func hostBuildPodDisposable(_ context.Context, req spec.PodDisposableRequest, _ buildEngineContext) (spec.PodDisposableReply, error) {
	cfg, err := deploykit.LoadBundleConfig()
	if err != nil || cfg == nil {
		// A missing/unreadable per-host overlay means the sandbox has no entry: not disposable,
		// not an error (the harness then skips its fresh-per-run restart) — the same graceful
		// degradation the former check-config projection made (it swallowed the load error).
		return spec.PodDisposableReply{}, nil
	}
	if entry, ok := cfg.Bundle[req.Name]; ok {
		return spec.PodDisposableReply{Disposable: entry.IsDisposable()}, nil
	}
	return spec.PodDisposableReply{}, nil
}

var _ = func() bool {
	registerHostBuilder(podDisposableBuilderKind, typedHostBuilder(podDisposableBuilderKind, hostBuildPodDisposable))
	return true
}()
