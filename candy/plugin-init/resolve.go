package initkind

// resolve.go — candy/plugin-init's OpResolve legs (the init de-type, Cutover F).
// The init-system KNOWLEDGE — how a service_template / drop-in renders into a
// systemd unit or supervisord fragment, plus the restart/stdout policy mappings —
// lives HERE now. The host builds the entry-derived ServiceRenderContext (home-
// expanded, branch decisions precomputed) and hands it + the opaque init body; this
// renders the unit. The host re-validates the returned body for egress.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"text/template"

	"github.com/opencharly/sdk/spec"
)

// renderServiceUnit renders one service into a RenderedService using the chosen
// init system's templates (the service-render leg).
func renderServiceUnit(in spec.ServiceRenderInput) (spec.ServiceRenderReply, error) {
	var initDef spec.Init
	if err := json.Unmarshal(in.Init, &initDef); err != nil {
		return spec.ServiceRenderReply{}, fmt.Errorf("init render: decode init: %w", err)
	}
	if initDef.ServiceSchema == nil {
		return spec.ServiceRenderReply{}, fmt.Errorf("init render: init system has no service_schema")
	}
	schema := initDef.ServiceSchema
	ctx := in.Ctx
	out := &spec.RenderedService{}

	// Packaged-unit branch: reuse a distro-shipped unit, render only a drop-in.
	if ctx.PackagedUnit != "" {
		if !schema.SupportsPackaged {
			return spec.ServiceRenderReply{}, fmt.Errorf("init system %q does not support use_packaged (entry %s)", initDef.ManagementTool, ctx.Name)
		}
		if ctx.RenderDropin {
			text, err := renderInitTemplate("service-dropin", schema.DropinTemplate, ctx)
			if err != nil {
				return spec.ServiceRenderReply{}, fmt.Errorf("rendering dropin for %s: %w", ctx.Name, err)
			}
			path, err := renderInitTemplate("dropin-path", schema.DropinPathTemplate, ctx)
			if err != nil {
				return spec.ServiceRenderReply{}, fmt.Errorf("rendering dropin path for %s: %w", ctx.Name, err)
			}
			out.DropinText = text
			out.DropinPath = strings.TrimSpace(path)
		}
		return spec.ServiceRenderReply{Rendered: out}, nil
	}

	// Custom unit branch.
	if schema.ServiceTemplate == "" {
		return spec.ServiceRenderReply{}, fmt.Errorf("init system %q has no service_template for custom entries", initDef.ManagementTool)
	}
	text, err := renderInitTemplate("service-unit", schema.ServiceTemplate, ctx)
	if err != nil {
		return spec.ServiceRenderReply{}, fmt.Errorf("rendering unit for %s: %w", ctx.Name, err)
	}
	path, err := renderInitTemplate("service-path", schema.UnitPathTemplate, ctx)
	if err != nil {
		return spec.ServiceRenderReply{}, fmt.Errorf("rendering unit path for %s: %w", ctx.Name, err)
	}
	out.UnitText = text
	out.UnitPath = strings.TrimSpace(path)
	return spec.ServiceRenderReply{Rendered: out}, nil
}

// resolveInitConfig projects an authored spec.Init into a ResolvedInit — the
// build/label/entrypoint value envelope the kernel consumes for legs 2–4. Raw is
// the opaque body threaded back to the service-render leg.
func resolveInitConfig(in spec.InitResolveInput) (spec.InitResolveReply, error) {
	var d spec.Init
	if err := json.Unmarshal(in.Init, &d); err != nil {
		return spec.InitResolveReply{}, fmt.Errorf("init resolve config: decode: %w", err)
	}
	return spec.InitResolveReply{Resolved: &spec.ResolvedInit{
		CandyFields:          d.CandyFields,
		CandyFiles:           d.CandyFiles,
		DependsCandy:         d.DependsCandy,
		RequiresCapability:   d.RequiresCapability,
		Model:                d.Model,
		HeaderFile:           d.HeaderFile,
		FragmentDir:          d.FragmentDir,
		RelayTemplate:        d.RelayTemplate,
		StageName:            d.StageName,
		StageHeaderCopy:      d.StageHeaderCopy,
		StageFragmentCopy:    d.StageFragmentCopy,
		AssemblyTemplate:     d.AssemblyTemplate,
		SystemEnableTemplate: d.SystemEnableTemplate,
		PostAssemblyTemplate: d.PostAssemblyTemplate,
		Entrypoint:           d.Entrypoint,
		FallbackEntrypoint:   d.FallbackEntrypoint,
		ManagementTool:       d.ManagementTool,
		ManagementCommands:   d.ManagementCommands,
		LabelKey:             d.LabelKey,
		ServiceSchema:        d.ServiceSchema,
		Raw:                  in.Init,
	}}, nil
}

func renderInitTemplate(name, tmpl string, data any) (string, error) {
	if tmpl == "" {
		return "", nil
	}
	t, err := template.New(name).Funcs(serviceRenderFuncs()).Parse(tmpl)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// serviceRenderFuncs are the init-system template funcs (the restart/stdout policy
// mappings each init system's service_template uses).
func serviceRenderFuncs() template.FuncMap {
	return template.FuncMap{
		"join": strings.Join,
		"derefBool": func(b *bool) bool {
			if b == nil {
				return false
			}
			return *b
		},
		"systemdRestart": func(r string) string {
			switch r {
			case "always":
				return "always"
			case "on-failure":
				return "on-failure"
			case "unless-stopped":
				return "always"
			case "no", "":
				return "no"
			}
			return "no"
		},
		"supervisordRestart": func(r string) string {
			switch r {
			case "always":
				return "true"
			case "on-failure":
				return "unexpected"
			case "unless-stopped":
				return "true"
			case "no", "":
				return "false"
			}
			return "false"
		},
		"systemdStdout": func(s string) string {
			if after, ok := strings.CutPrefix(s, "file:"); ok {
				return "append:" + after
			}
			if s == "" {
				return "journal"
			}
			return s
		},
		"supervisordLog": func(s string) string {
			if after, ok := strings.CutPrefix(s, "file:"); ok {
				return after
			}
			switch s {
			case "none":
				return "/dev/null"
			case "journal", "":
				return "/dev/fd/1"
			}
			return s
		},
		"supervisordLogMaxbytes": func(s string) string {
			if strings.HasPrefix(s, "file:") {
				return "10MB"
			}
			return "0"
		},
	}
}
