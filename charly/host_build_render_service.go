package main

import (
	"context"

	"github.com/opencharly/sdk/spec"
)

// host_build_render_service.go — the "render-service" F10 host-builder (K5-A item 1
// increment B, compile-seam ctx-threading): the ONE genuinely host-only piece of the
// former deploykit.CompileServiceSteps — rendering a systemd CUSTOM service entry's
// unit text via charly/service_render.go:RenderService, which itself wraps TWO
// registry consults a plugin cannot do (candy/plugin-init's OpResolve + the M16
// egress gate). RenderService's own body is UNCHANGED; only its caller moved
// (deploykit.CompileServiceSteps now reaches it over this seam instead of a direct
// in-process call).
const renderServiceBuilderKind = "render-service"

func hostBuildRenderService(_ context.Context, req spec.RenderServiceRequest, _ buildEngineContext) (spec.RenderServiceReply, error) {
	rendered, err := RenderService(&req.Entry, &req.Init, req.Ctx)
	if err != nil {
		return spec.RenderServiceReply{}, err
	}
	return spec.RenderServiceReply{Rendered: rendered}, nil
}

var _ = func() bool {
	registerHostBuilder(renderServiceBuilderKind, typedHostBuilder(renderServiceBuilderKind, hostBuildRenderService))
	return true
}()
