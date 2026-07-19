package main

// service_render.go — the HOST side of service materialization after the init
// de-type (Cutover F, leg 1). The init-system KNOWLEDGE — how a service_template /
// drop-in renders into a systemd unit or supervisord fragment, plus the restart/
// stdout policy mappings — lives in candy/plugin-init's OpResolve. The host builds
// the entry-derived, home-expanded ServiceRenderContext (pure ServiceEntry
// projection, no init knowledge) and calls the plugin, then egress-validates.

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// The render types are shared spec envelopes (host builds them, plugin renders).
// ResolvedInit is the init de-type's build/label/entrypoint value envelope the
// kernel consumes instead of the concrete spec.Init.
type (
	ServiceRenderContext = spec.ServiceRenderContext
	RenderedService      = spec.RenderedService
	KeyValue             = spec.KeyValue
	ResolvedInit         = spec.ResolvedInit
)

// RenderService renders a ServiceEntry into a RenderedService via candy/plugin-init.
// (Transitional: the typed initDef is marshalled to the plugin; the init config goes
// fully opaque in F's finalize step.)
func RenderService(entry *spec.ServiceEntry, def *ResolvedInit, ctx ServiceRenderContext) (*RenderedService, error) {
	if entry == nil {
		return nil, fmt.Errorf("RenderService: nil entry")
	}
	if def == nil || def.ServiceSchema == nil {
		return nil, fmt.Errorf("RenderService: init system has no service_schema")
	}
	ctx = deploykit.BuildServiceRenderContext(entry, ctx)
	rendered, err := renderServiceViaPlugin(spec.ServiceRenderInput{Init: def.Raw, Ctx: ctx})
	if err != nil {
		return nil, err
	}
	// Egress gate (host-side): a template-render failure leaves the "<no value>"
	// marker in the unit body — reject it before the unit is written.
	if rendered.UnitText != "" {
		if err := validateTextEgress("service-unit:"+entry.Name, rendered.UnitText); err != nil {
			return nil, err
		}
	}
	return rendered, nil
}

// renderServiceViaPlugin invokes candy/plugin-init's OpResolve service-render leg.
func renderServiceViaPlugin(in spec.ServiceRenderInput) (*RenderedService, error) {
	out, err := invokeInitResolve(spec.InitResolveRequest{Render: &in})
	if err != nil {
		return nil, err
	}
	var reply spec.ServiceRenderReply
	if len(out) > 0 {
		if err := json.Unmarshal(out, &reply); err != nil {
			return nil, fmt.Errorf("init render: decode reply: %w", err)
		}
	}
	if reply.Rendered == nil {
		return &RenderedService{}, nil
	}
	return reply.Rendered, nil
}

// resolveInitConfigViaPlugin invokes candy/plugin-init's OpResolve config leg,
// projecting one opaque init body into a *ResolvedInit (legs 2–4 value envelope).
func resolveInitConfigViaPlugin(body json.RawMessage) (*ResolvedInit, error) {
	out, err := invokeInitResolve(spec.InitResolveRequest{Config: &spec.InitResolveInput{Init: body}})
	if err != nil {
		return nil, err
	}
	var reply spec.InitResolveReply
	if len(out) > 0 {
		if err := json.Unmarshal(out, &reply); err != nil {
			return nil, fmt.Errorf("init resolve config: decode reply: %w", err)
		}
	}
	return reply.Resolved, nil
}

// invokeInitResolve dispatches an OpResolve request to the compiled-in init kind
// provider (both legs share it).
func invokeInitResolve(req spec.InitResolveRequest) ([]byte, error) {
	prov, ok := providerRegistry.ResolveKind("init")
	if !ok {
		return nil, fmt.Errorf("init resolve: kind provider not registered")
	}
	return invokeTyped[spec.InitResolveRequest, json.RawMessage](context.Background(), prov, "init", OpResolve, req)
}
