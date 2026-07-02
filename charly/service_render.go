package main

// service_render.go — generic service schema + per-init-system renderer.
//
// Today's candy manifest has two separate service fields:
//   - service: (raw supervisord INI fragment — container-only)
//   - system_services: (list of systemd unit names to enable)
//
// Both collapse into one unified `services:` schema keyed by service
// name. Each entry is either `use_packaged: <unit>` (reuse a distro-
// shipped systemd unit with optional drop-in overrides) or a fully
// structured spec (`exec`, `env`, `restart`, `after`, `before`,
// `scope`, …) that gets rendered by the init-system's service_template
// in the embedded `init:` vocabulary (charly/charly.yml).
//
// This file declares the schema types, the rendering context, and the
// template rendering helpers. It does NOT parse the candy manifest — that
// happens via the CUE-decode loader (cue_loader.go: decodeEntityViaCUE).

import (
	"bytes"
	"fmt"
	"maps"
	"sort"
	"strings"
	"text/template"
)

// ---------------------------------------------------------------------------
// ServiceRenderContext — data passed to init-system service_template.
// ---------------------------------------------------------------------------

// ServiceRenderContext is the template-rendering context exposed to
// the embedded `init:` vocabulary's init.<name>.service_template and its siblings. Keeps the
// interface surface tight: anything the renderer needs is a field here;
// nothing else is reachable.
type ServiceRenderContext struct {
	Name             string
	Candy            string
	Exec             string
	Env              map[string]string
	EnvList          []KeyValue // ordered env for deterministic template iteration
	Restart          string
	WorkingDirectory string
	User             string
	After            []string
	Before           []string
	WantedBy         []string // [Install] WantedBy override; empty → scope default
	Stdout           string
	StopTimeout      string
	Scope            string // "system" | "user"
	PackagedUnit     string // non-empty for drop-in rendering
	Home             string // invoking user's home — for user-scope unit paths
	SystemUnitDir    string // e.g. "/etc/systemd/system"
	UserUnitDir      string // e.g. "/home/user/.config/systemd/user"
	FragmentDir      string // supervisord fragment directory

	// Lifecycle directives (supervisord + systemd). See ServiceEntry for semantics.
	Kind         string
	Events       string
	AutoStart    *bool
	StartRetries int
	StartSecs    int
	StopSignal   string
	ExitCodes    string
	Priority     int
}

// KeyValue is a deterministic env-var ordering helper.
type KeyValue struct {
	Key   string
	Value string
}

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------

// RenderedService is the output of the renderer: the unit text, the
// path it should land at, and any drop-in path for packaged reuse.
type RenderedService struct {
	UnitText   string // "" for packaged-only entries (no body, just enable)
	UnitPath   string // where to write UnitText
	DropinText string // "" when no Overrides
	DropinPath string // drop-in file path when DropinText is non-empty
}

// RenderService turns a ServiceEntry into a RenderedService using the
// given init system's templates. Returns an error when the chosen init
// system has no template for the entry's shape (custom unit on
// supervisord-only init, packaged unit on supervisord, etc.).
func RenderService(entry *ServiceEntry, initDef *InitDef, ctx ServiceRenderContext) (*RenderedService, error) {
	if entry == nil {
		return nil, fmt.Errorf("RenderService: nil entry")
	}
	if initDef == nil || initDef.ServiceSchema == nil {
		return nil, fmt.Errorf("RenderService: init system has no service_schema")
	}
	schema := initDef.ServiceSchema
	out := &RenderedService{}
	ctx.Name = entry.Name
	// ctx.Candy is preserved from the caller (not overwritten here).
	ctx.Scope = entry.EffectiveScope()
	ctx.PackagedUnit = entry.UsePackaged
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
	// Make home-relative exec/working-dir/env portable across init systems.
	// supervisord expands its own `%(ENV_HOME)s` at runtime; systemd does NOT,
	// so a candy whose service exec reuses that syntax (or a bare $HOME) yields
	// a broken ExecStart on a systemd target. Resolve both against ctx.Home —
	// which the compiler sets to the deferred {{.Home}} token for host/vm
	// targets (substituted per-destination by InstallPlan.ResolveHome at emit)
	// and to the image's runtime home for a container-systemd build. No-op for
	// the common case of absolute exec paths.
	if ctx.Home != "" {
		homify := func(s string) string {
			// supervisord's own %(ENV_HOME)s first, then ~ / ${HOME} / $HOME via
			// ExpandPath (the braced ${HOME} form is what kde-selkies/labwc-style
			// exec lines use — a bare $HOME ReplaceAll would miss it).
			s = strings.ReplaceAll(s, "%(ENV_HOME)s", ctx.Home)
			return ExpandPath(s, ctx.Home)
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
	// Lifecycle directives — passed through verbatim to the init-system template.
	ctx.Kind = entry.Kind
	ctx.Events = entry.Events
	ctx.AutoStart = entry.AutoStart
	ctx.StartRetries = entry.StartRetries
	ctx.StartSecs = entry.StartSecs
	ctx.StopSignal = entry.StopSignal
	ctx.ExitCodes = entry.ExitCode
	ctx.Priority = entry.Priority

	if entry.IsPackaged() {
		if !schema.SupportsPackaged {
			return nil, fmt.Errorf("init system %q does not support use_packaged (entry %s)", initDef.ManagementTool, entry.Name)
		}
		// Only the drop-in branch renders — no new unit body.
		if entry.Overrides != nil {
			text, err := renderTemplateString("service-dropin", schema.DropinTemplate, ctx)
			if err != nil {
				return nil, fmt.Errorf("rendering dropin for %s: %w", entry.Name, err)
			}
			path, err := renderTemplateString("dropin-path", schema.DropinPathTemplate, ctx)
			if err != nil {
				return nil, fmt.Errorf("rendering dropin path for %s: %w", entry.Name, err)
			}
			out.DropinText = text
			out.DropinPath = strings.TrimSpace(path)
		}
		return out, nil
	}

	// Custom unit path
	if schema.ServiceTemplate == "" {
		return nil, fmt.Errorf("init system %q has no service_template for custom entries", initDef.ManagementTool)
	}
	text, err := renderTemplateString("service-unit", schema.ServiceTemplate, ctx)
	if err != nil {
		return nil, fmt.Errorf("rendering unit for %s: %w", entry.Name, err)
	}
	path, err := renderTemplateString("service-path", schema.UnitPathTemplate, ctx)
	if err != nil {
		return nil, fmt.Errorf("rendering unit path for %s: %w", entry.Name, err)
	}
	out.UnitText = text
	out.UnitPath = strings.TrimSpace(path)
	// Egress gate: a template render failure leaves the "<no value>" marker in the
	// unit body — reject it before the unit is written (see /charly-internals:egress).
	if out.UnitText != "" {
		if err := validateTextEgress("service-unit:"+entry.Name, out.UnitText); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// sortedEnvList returns a sorted-by-key slice of env entries.
// Deterministic ordering matters for template rendering — tests compare
// rendered output directly against golden strings.
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

// flattenedEnvMap composes base + overrides into one map, with
// overrides winning on conflict. Returns a fresh map; callers don't
// mutate base.
func flattenedEnvMap(base map[string]string, overrides *ServiceOverrides) map[string]string {
	out := make(map[string]string, len(base))
	maps.Copy(out, base)
	if overrides != nil {
		maps.Copy(out, overrides.Env)
	}
	return out
}

// renderTemplateString executes a Go text/template with the standard
// helper funcs (join, supervisordRestart, systemdRestart, systemdStdout)
// plus whatever the caller's context provides.
func renderTemplateString(name, tmpl string, data any) (string, error) {
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

// serviceRenderFuncs returns the per-init-system helper functions
// referenced in the embedded init-system templates. Adding a new init system means
// adding its helpers here so its templates stay readable.
func serviceRenderFuncs() template.FuncMap {
	return template.FuncMap{
		"join": strings.Join,
		// derefBool dereferences a *bool for template conditionals —
		// callers check `{{if .AutoStart}}` for "explicitly set" then
		// `{{derefBool .AutoStart}}` to get the true/false value.
		"derefBool": func(b *bool) bool {
			if b == nil {
				return false
			}
			return *b
		},
		// systemdRestart maps the abstract `restart:` keyword to
		// systemd's Restart= policy.
		"systemdRestart": func(r string) string {
			switch r {
			case "always":
				return "always"
			case "on-failure":
				return "on-failure"
			case "unless-stopped":
				// systemd has no direct equivalent; `always` is the
				// closest semantic match (restart even on clean exit).
				return "always"
			case "no", "":
				return "no"
			}
			return "no"
		},
		// supervisordRestart maps `restart:` to supervisord's
		// autorestart= value.
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
		// systemdStdout maps `stdout:` to systemd StandardOutput=.
		// "file:/path" renders as append:/path; everything else maps
		// directly.
		"systemdStdout": func(s string) string {
			if after, ok := strings.CutPrefix(s, "file:"); ok {
				return "append:" + after
			}
			if s == "" {
				return "journal"
			}
			return s
		},
		// supervisordLog maps the abstract `stdout:` keyword to supervisord's
		// stdout_logfile= value. "file:/path" → a dedicated rotating log file;
		// "none" → /dev/null; "journal"/unset → /dev/fd/1 (the container's own
		// stdout, the long-standing default, so services that don't set stdout:
		// are unchanged).
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
		// supervisordLogMaxbytes pairs with supervisordLog: a real log file
		// rotates (10MB), but the special files /dev/fd/1 and /dev/null MUST be
		// maxbytes=0 — supervisord rejects rotation on non-seekable targets.
		"supervisordLogMaxbytes": func(s string) string {
			if strings.HasPrefix(s, "file:") {
				return "10MB"
			}
			return "0"
		},
	}
}
