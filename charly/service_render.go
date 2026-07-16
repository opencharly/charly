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
	"maps"
	"sort"
	"strings"

	"github.com/opencharly/sdk/kit"
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
	ctx = buildServiceRenderContext(entry, ctx)
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

// buildServiceRenderContext fills the entry-derived, home-expanded render context
// (a pure ServiceEntry projection — NO init-system knowledge). The plugin renders
// its templates against this; the packaged/drop-in branch decisions are precomputed
// here (PackagedUnit, RenderDropin) so the plugin renders from the ctx alone.
func buildServiceRenderContext(entry *spec.ServiceEntry, ctx ServiceRenderContext) ServiceRenderContext {
	ctx.Name = entry.Name
	ctx.Scope = entry.EffectiveScope()
	ctx.PackagedUnit = entry.UsePackaged
	ctx.RenderDropin = entry.Overrides != nil
	ctx.Env = flattenedEnvMap(entry.Env, entry.Overrides)
	ctx.EnvList = sortedEnvList(ctx.Env)
	if entry.Exec != "" {
		ctx.Exec = entry.Exec
	}
	if entry.Overrides != nil && entry.Overrides.Exec != "" {
		ctx.Exec = entry.Overrides.Exec
	}
	if entry.WorkingDirectory != "" {
		ctx.WorkingDirectory = entry.WorkingDirectory
	}
	// Make home-relative exec/working-dir/env portable across init systems
	// (supervisord's %(ENV_HOME)s + ~ / ${HOME} / $HOME), resolved against ctx.Home.
	if ctx.Home != "" {
		homify := func(s string) string {
			s = strings.ReplaceAll(s, "%(ENV_HOME)s", ctx.Home)
			return kit.ExpandPath(s, ctx.Home)
		}
		ctx.Exec = homify(ctx.Exec)
		ctx.WorkingDirectory = homify(ctx.WorkingDirectory)
		for k, v := range ctx.Env {
			ctx.Env[k] = homify(v)
		}
		ctx.EnvList = sortedEnvList(ctx.Env)
	}
	if entry.User != "" {
		ctx.User = entry.User
	}
	ctx.After = append(ctx.After, entry.After...)
	if entry.Overrides != nil {
		ctx.After = append(ctx.After, entry.Overrides.After...)
	}
	ctx.Before = append(ctx.Before, entry.Before...)
	ctx.WantedBy = entry.WantedBy
	ctx.Restart = entry.Restart
	ctx.Stdout = entry.Stdout
	ctx.StopTimeout = entry.StopTimeout
	ctx.Kind = entry.Kind
	ctx.Events = entry.Events
	ctx.AutoStart = entry.AutoStart
	ctx.StartRetries = entry.StartRetries
	ctx.StartSecs = entry.StartSecs
	ctx.StopSignal = entry.StopSignal
	ctx.ExitCodes = entry.ExitCode
	ctx.Priority = entry.Priority
	return ctx
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

// sortedEnvList returns a sorted-by-key slice of env entries. Deterministic ordering
// matters for template rendering — tests compare rendered output directly.
func sortedEnvList(env map[string]string) []KeyValue {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]KeyValue, 0, len(keys))
	for _, k := range keys {
		out = append(out, KeyValue{Key: k, Value: env[k]})
	}
	return out
}

// flattenedEnvMap composes base + overrides into one map (overrides win). Returns a
// fresh map; callers don't mutate base.
func flattenedEnvMap(base map[string]string, overrides *ServiceOverrides) map[string]string {
	out := make(map[string]string, len(base))
	maps.Copy(out, base)
	if overrides != nil {
		maps.Copy(out, overrides.Env)
	}
	return out
}
