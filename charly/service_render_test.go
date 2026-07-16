package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
	"github.com/opencharly/sdk/vmshared"
)

// Tests for service_render.go.
//
// These drive RenderService against synthetic InitDef + ServiceEntry
// pairs to verify the (packaged vs custom) × (systemd vs supervisord)
// matrix. Templates are minimal inline snippets so the assertions focus
// on the correct branching and substitution logic, not the full
// build.yml template contents.

const testSystemdServiceTemplate = `[Unit]
Description=charly: {{.Candy}} {{.Name}}
{{with .After}}After={{join . " "}}{{end}}

[Service]
ExecStart={{.Exec}}
{{range .EnvList}}Environment="{{.Key}}={{.Value}}"
{{end}}Restart={{systemdRestart .Restart}}

[Install]
WantedBy={{if .WantedBy}}{{join .WantedBy " "}}{{else if eq .Scope "system"}}multi-user.target{{else}}default.target{{end}}
`

const testSystemdUnitPathTemplate = `{{- if eq .Scope "system" -}}
{{.SystemUnitDir}}/charly-{{.Candy}}-{{.Name}}.service
{{- else -}}
{{.UserUnitDir}}/charly-{{.Candy}}-{{.Name}}.service
{{- end -}}`

const testSystemdDropinTemplate = `[Service]
{{range .EnvList}}Environment="{{.Key}}={{.Value}}"
{{end}}{{with .After}}After={{join . " "}}{{end}}
`

const testSystemdDropinPathTemplate = `{{- if eq .Scope "system" -}}
{{.SystemUnitDir}}/{{.PackagedUnit}}.d/charly-{{.Candy}}.conf
{{- else -}}
{{.UserUnitDir}}/{{.PackagedUnit}}.d/charly-{{.Candy}}.conf
{{- end -}}`

const testSupervisordServiceTemplate = `[program:{{.Name}}]
command={{.Exec}}
autorestart={{supervisordRestart .Restart}}
`

// withRaw sets .Raw to the fixture's own marshalled JSON — since ResolvedInit's
// tags match spec.Init, the plugin render decodes .Raw back into a spec.Init with
// the ServiceSchema, exactly as production does.
func withRaw(ri *ResolvedInit) *ResolvedInit {
	ri.Raw, _ = json.Marshal(ri)
	return ri
}

func testSystemdInitDef() *ResolvedInit {
	return withRaw(&ResolvedInit{
		ManagementTool: "systemctl",
		ServiceSchema: &vmshared.ServiceSchemaDef{
			ServiceTemplate:    testSystemdServiceTemplate,
			UnitPathTemplate:   testSystemdUnitPathTemplate,
			DropinTemplate:     testSystemdDropinTemplate,
			DropinPathTemplate: testSystemdDropinPathTemplate,
			SupportsPackaged:   true,
		},
	})
}

func testSupervisordInitDef() *ResolvedInit {
	return withRaw(&ResolvedInit{
		ManagementTool: "supervisorctl",
		ServiceSchema: &vmshared.ServiceSchemaDef{
			ServiceTemplate:  testSupervisordServiceTemplate,
			UnitPathTemplate: `/etc/supervisord.d/{{.Candy}}-{{.Name}}.conf`,
			SupportsPackaged: false,
		},
	})
}

func TestRenderServiceCustomSystemd(t *testing.T) {
	entry := &spec.ServiceEntry{
		Name:       "ollama",
		Exec:       "/usr/bin/ollama serve",
		Env:        map[string]string{"OLLAMA_HOST": "0.0.0.0:11434"},
		Restart:    "always",
		After:      []string{"network.target"},
		Scope: "system",
		Enable:     true,
	}
	rendered, err := RenderService(entry, testSystemdInitDef(), ServiceRenderContext{
		Candy:         "ollama",
		SystemUnitDir: "/etc/systemd/system",
	})
	if err != nil {
		t.Fatalf("RenderService: %v", err)
	}
	if !strings.Contains(rendered.UnitText, "ExecStart=/usr/bin/ollama serve") {
		t.Errorf("missing ExecStart; got:\n%s", rendered.UnitText)
	}
	if !strings.Contains(rendered.UnitText, `Environment="OLLAMA_HOST=0.0.0.0:11434"`) {
		t.Errorf("missing Environment entry; got:\n%s", rendered.UnitText)
	}
	if !strings.Contains(rendered.UnitText, "Restart=always") {
		t.Errorf("missing Restart=always; got:\n%s", rendered.UnitText)
	}
	if !strings.Contains(rendered.UnitText, "After=network.target") {
		t.Errorf("missing After=network.target; got:\n%s", rendered.UnitText)
	}
	if rendered.UnitPath != "/etc/systemd/system/charly-ollama-ollama.service" {
		t.Errorf("UnitPath = %q, want /etc/systemd/system/charly-ollama-ollama.service", rendered.UnitPath)
	}
	if rendered.DropinText != "" {
		t.Errorf("custom entry should have empty DropinText; got %q", rendered.DropinText)
	}
}

// A user service with an explicit wanted_by must enable into THAT target
// (graphical-session.target) rather than the user default — so a
// graphical-session-scoped service is pulled WITH the logged-in session, not at
// early user-manager start (where the Wayland display doesn't yet exist).
func TestRenderServiceWantedBy(t *testing.T) {
	entry := &spec.ServiceEntry{
		Name:       "session-capture",
		Exec:       "/usr/bin/session-capture",
		Restart:    "always",
		Scope: "user",
		Enable:     true,
		After:      []string{"graphical-session.target"},
		WantedBy:   []string{"graphical-session.target"},
	}
	rendered, err := RenderService(entry, testSystemdInitDef(), ServiceRenderContext{
		Candy:       "session-capture",
		UserUnitDir: "/home/cachy/.config/systemd/user",
	})
	if err != nil {
		t.Fatalf("RenderService: %v", err)
	}
	if !strings.Contains(rendered.UnitText, "WantedBy=graphical-session.target") {
		t.Errorf("missing WantedBy=graphical-session.target; got:\n%s", rendered.UnitText)
	}
	if strings.Contains(rendered.UnitText, "WantedBy=default.target") {
		t.Errorf("user-default WantedBy leaked despite explicit wanted_by; got:\n%s", rendered.UnitText)
	}
}

// A service exec that reuses supervisord's %(ENV_HOME)s syntax (or a bare
// $HOME) must render a USABLE systemd ExecStart. With ctx.Home set to the
// deferred {{.Home}} token (what the compiler passes for host/vm targets),
// both spellings resolve to the token so InstallPlan.ResolveHome can
// substitute the real destination home at emit — not the build host's home.
func TestRenderServiceHomePortabilityToken(t *testing.T) {
	entry := &spec.ServiceEntry{
		Name:       "selkies",
		Exec:       "python3 %(ENV_HOME)s/.local/bin/selkies-capture-server",
		Env:        map[string]string{"SELKIES_DATA": "$HOME/.config/selkies"},
		Scope: "user",
		Enable:     true,
	}
	rendered, err := RenderService(entry, testSystemdInitDef(), ServiceRenderContext{
		Candy:       "selkies",
		Home:        deploykit.HomeToken,
		UserUnitDir: deploykit.HomeToken + "/.config/systemd/user",
	})
	if err != nil {
		t.Fatalf("RenderService: %v", err)
	}
	if !strings.Contains(rendered.UnitText, "ExecStart=python3 {{.Home}}/.local/bin/selkies-capture-server") {
		t.Errorf("%%(ENV_HOME)s not translated to the home token; got:\n%s", rendered.UnitText)
	}
	if strings.Contains(rendered.UnitText, "%(ENV_HOME)s") {
		t.Errorf("raw supervisord %%(ENV_HOME)s leaked into the systemd unit:\n%s", rendered.UnitText)
	}
	if !strings.Contains(rendered.UnitText, `Environment="SELKIES_DATA={{.Home}}/.config/selkies"`) {
		t.Errorf("$HOME in env not resolved to the home token; got:\n%s", rendered.UnitText)
	}
	// The user-scope unit path is also home-relative → carries the token.
	if !strings.Contains(rendered.UnitPath, "{{.Home}}/.config/systemd/user/") {
		t.Errorf("user-scope UnitPath should carry the home token; got %q", rendered.UnitPath)
	}

	// Emit-time resolution: a ServiceCustomStep carrying that text resolves to
	// the real guest home, not the operator's.
	plan := &deploykit.InstallPlan{Steps: []spec.InstallStep{
		&deploykit.ServiceCustomStep{Name: "charly-selkies-selkies", UnitText: rendered.UnitText, UnitPath: rendered.UnitPath, TargetScope: spec.ScopeUser},
	}}
	deploykit.ResolveHome(plan, "/home/cachy")
	cs := plan.Steps[0].(*deploykit.ServiceCustomStep)
	if !strings.Contains(cs.UnitText, "ExecStart=python3 /home/cachy/.local/bin/selkies-capture-server") {
		t.Errorf("ResolveHome did not substitute the unit ExecStart; got:\n%s", cs.UnitText)
	}
	if !strings.Contains(cs.UnitPath, "/home/cachy/.config/systemd/user/") {
		t.Errorf("ResolveHome did not substitute the unit path; got %q", cs.UnitPath)
	}
	if strings.Contains(cs.UnitText, "{{.Home}}") {
		t.Errorf("home token survived ResolveHome:\n%s", cs.UnitText)
	}
}

func TestRenderServicePackagedWithOverrides(t *testing.T) {
	entry := &spec.ServiceEntry{
		Name:        "postgresql",
		UsePackaged: "postgresql.service",
		Enable:      true,
		Scope:  "system",
		Overrides: &ServiceOverrides{
			Env: map[string]string{"PGDATA": "/var/lib/postgresql/data"},
		},
	}
	rendered, err := RenderService(entry, testSystemdInitDef(), ServiceRenderContext{
		Candy:         "postgresql",
		SystemUnitDir: "/etc/systemd/system",
	})
	if err != nil {
		t.Fatalf("RenderService: %v", err)
	}
	if rendered.UnitText != "" {
		t.Errorf("packaged entry should have empty UnitText; got %q", rendered.UnitText)
	}
	if !strings.Contains(rendered.DropinText, `Environment="PGDATA=/var/lib/postgresql/data"`) {
		t.Errorf("missing drop-in env; got:\n%s", rendered.DropinText)
	}
	want := "/etc/systemd/system/postgresql.service.d/charly-postgresql.conf"
	if rendered.DropinPath != want {
		t.Errorf("DropinPath = %q, want %q", rendered.DropinPath, want)
	}
}

func TestRenderServicePackagedOnSupervisordRefuses(t *testing.T) {
	entry := &spec.ServiceEntry{
		Name:        "postgresql",
		UsePackaged: "postgresql.service",
		Enable:      true,
	}
	_, err := RenderService(entry, testSupervisordInitDef(), ServiceRenderContext{Candy: "pg"})
	if err == nil {
		t.Fatalf("expected error rendering use_packaged on supervisord, got nil")
	}
	if !strings.Contains(err.Error(), "use_packaged") {
		t.Errorf("error message doesn't mention use_packaged: %v", err)
	}
}

func TestRenderServiceCustomSupervisord(t *testing.T) {
	entry := &spec.ServiceEntry{
		Name:    "ollama",
		Exec:    "/usr/bin/ollama serve",
		Restart: "always",
	}
	rendered, err := RenderService(entry, testSupervisordInitDef(), ServiceRenderContext{Candy: "ollama"})
	if err != nil {
		t.Fatalf("RenderService: %v", err)
	}
	if !strings.Contains(rendered.UnitText, "[program:ollama]") {
		t.Errorf("missing [program:ollama]; got:\n%s", rendered.UnitText)
	}
	if !strings.Contains(rendered.UnitText, "command=/usr/bin/ollama serve") {
		t.Errorf("missing command=; got:\n%s", rendered.UnitText)
	}
	if !strings.Contains(rendered.UnitText, "autorestart=true") {
		t.Errorf("autorestart mapping wrong; got:\n%s", rendered.UnitText)
	}
}

func TestRenderServiceUserScope(t *testing.T) {
	entry := &spec.ServiceEntry{
		Name:       "x",
		Exec:       "/bin/true",
		Scope: "user",
		Enable:     true,
	}
	rendered, err := RenderService(entry, testSystemdInitDef(), ServiceRenderContext{
		Candy:       "x",
		UserUnitDir: "/home/user/.config/systemd/user",
	})
	if err != nil {
		t.Fatalf("RenderService: %v", err)
	}
	if !strings.Contains(rendered.UnitText, "WantedBy=default.target") {
		t.Errorf("user-scope unit should WantedBy=default.target; got:\n%s", rendered.UnitText)
	}
	want := "/home/user/.config/systemd/user/charly-x-x.service"
	if rendered.UnitPath != want {
		t.Errorf("UnitPath = %q, want %q", rendered.UnitPath, want)
	}
}

func TestServiceEntryIsPackaged(t *testing.T) {
	packaged := &spec.ServiceEntry{UsePackaged: "foo.service"}
	custom := &spec.ServiceEntry{Exec: "/bin/foo"}
	if !packaged.IsPackaged() {
		t.Errorf("packaged entry should return IsPackaged=true")
	}
	if custom.IsPackaged() {
		t.Errorf("custom entry should return IsPackaged=false")
	}
	var nilEntry *spec.ServiceEntry
	if nilEntry.IsPackaged() {
		t.Errorf("nil entry should return IsPackaged=false")
	}
}

func TestServiceEntryEffectiveScope(t *testing.T) {
	if got := (&spec.ServiceEntry{}).EffectiveScope(); got != "system" {
		t.Errorf("default scope = %q, want system", got)
	}
	if got := (&spec.ServiceEntry{Scope: "user"}).EffectiveScope(); got != "user" {
		t.Errorf("explicit user scope = %q", got)
	}
}
