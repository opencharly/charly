package main

import (
	"context"

	"github.com/opencharly/sdk/spec"
)

// host_build_ephemeral_register.go — the "ephemeral-register" F10 host-builder (FINAL/K5 unit
// 6a): wraps registerEphemeralIfMarked (charly/deploy_add_shared.go) verbatim behind a generic,
// substrate-agnostic HostBuild seam, so a substrate whose venue-lifecycle PrepareVenue body now
// lives in its OWN plugin (candy/plugin-deploy-vm, replacing the deleted lifecyclePrepareHook
// indirection) can still trigger the ONE Add-time host side effect it cannot do itself: the
// systemd transient-timer registration + panic-vs-warning classification (RCA #5) that must stay
// single-sourced host-side, where the add-failure semantics live. The seam kind is a generic
// action noun (F11) — never a substrate word — so pod/k8s's own future ephemeral support (the
// bed-robustness batch) reaches it through the identical hop, no new mechanism per substrate.
const ephemeralRegisterBuilderKind = "ephemeral-register"

func hostBuildEphemeralRegister(_ context.Context, req spec.EphemeralRegisterRequest, _ buildEngineContext) (spec.EphemeralRegisterReply, error) {
	if err := registerEphemeralIfMarked(req.Node, req.Name); err != nil {
		return spec.EphemeralRegisterReply{}, err
	}
	return spec.EphemeralRegisterReply{}, nil
}

var _ = func() bool {
	registerHostBuilder(ephemeralRegisterBuilderKind, typedHostBuilder(ephemeralRegisterBuilderKind, hostBuildEphemeralRegister))
	return true
}()
