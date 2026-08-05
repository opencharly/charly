package initkind

// render_service_unit_test.go — the (packaged vs custom) x (systemd vs supervisord) service
// render matrix, relocated from charly/service_render_test.go (#55 W3 B4). These tests always
// exercised THIS package's own renderServiceUnit — charly's former RenderService was a thin
// registry-consult wrapper (BuildServiceRenderContext + a providerRegistry.ResolveKind("init")
// dispatch) around exactly the function tested here; the actual template-render / packaged-vs-
// custom / systemd-vs-supervisord branching logic the assertions below cover has ALWAYS lived in
// this package (resolve.go's renderServiceUnit + serviceRenderFuncs). R3 — test the code where it
// lives: charly core no longer holds a front door to this logic at all (RenderService/
// renderServiceViaPlugin are deleted), so this coverage moves here rather than being lost.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/opencharly/spec/spec"
)

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

// withRaw sets .Raw to the fixture's own marshalled JSON — since ResolvedInit's tags match
// spec.Init, renderServiceUnit decodes .Raw back into a spec.Init with the ServiceSchema, exactly
// as production does (candy/plugin-init/resolve.go's resolveInitConfig produces this same Raw
// shape from an authored spec.Init).
func withRaw(ri *spec.ResolvedInit) *spec.ResolvedInit {
	ri.Raw, _ = json.Marshal(ri)
	return ri
}

func testSystemdInitDef() *spec.ResolvedInit {
	return withRaw(&spec.ResolvedInit{
		ManagementTool: "systemctl",
		ServiceSchema: &spec.InitServiceSchema{
			ServiceTemplate:    testSystemdServiceTemplate,
			UnitPathTemplate:   testSystemdUnitPathTemplate,
			DropinTemplate:     testSystemdDropinTemplate,
			DropinPathTemplate: testSystemdDropinPathTemplate,
			SupportsPackaged:   true,
		},
	})
}

func testSupervisordInitDef() *spec.ResolvedInit {
	return withRaw(&spec.ResolvedInit{
		ManagementTool: "supervisorctl",
		ServiceSchema: &spec.InitServiceSchema{
			ServiceTemplate:  testSupervisordServiceTemplate,
			UnitPathTemplate: `/etc/supervisord.d/{{.Candy}}-{{.Name}}.conf`,
			SupportsPackaged: false,
		},
	})
}

// renderService is the test-local front door replacing the deleted charly-core RenderService: it
// performs the SAME BuildServiceRenderContext fold + renderServiceUnit dispatch, in-process, no
// wire hop — mirroring exactly what candy/plugin-init's real Invoke handler + REAL callers
// (sdk/deploykit's renderSeamCaller.renderService) do.
func renderService(entry *spec.ServiceEntry, def *spec.ResolvedInit, ctx spec.ServiceRenderContext) (*spec.RenderedService, error) {
	ctx = spec.BuildServiceRenderContext(entry, ctx)
	reply, err := renderServiceUnit(spec.ServiceRenderInput{Init: def.Raw, Ctx: ctx})
	if err != nil {
		return nil, err
	}
	if reply.Rendered == nil {
		return &spec.RenderedService{}, nil
	}
	return reply.Rendered, nil
}

func TestRenderServiceCustomSystemd(t *testing.T) {
	entry := &spec.ServiceEntry{
		Name:    "ollama",
		Exec:    "/usr/bin/ollama serve",
		Env:     map[string]string{"OLLAMA_HOST": "0.0.0.0:11434"},
		Restart: "always",
		After:   []string{"network.target"},
		Scope:   "system",
		Enable:  true,
	}
	rendered, err := renderService(entry, testSystemdInitDef(), spec.ServiceRenderContext{
		Candy:         "ollama",
		SystemUnitDir: "/etc/systemd/system",
	})
	if err != nil {
		t.Fatalf("renderService: %v", err)
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
		Name:     "session-capture",
		Exec:     "/usr/bin/session-capture",
		Restart:  "always",
		Scope:    "user",
		Enable:   true,
		After:    []string{"graphical-session.target"},
		WantedBy: []string{"graphical-session.target"},
	}
	rendered, err := renderService(entry, testSystemdInitDef(), spec.ServiceRenderContext{
		Candy:       "session-capture",
		UserUnitDir: "/home/cachy/.config/systemd/user",
	})
	if err != nil {
		t.Fatalf("renderService: %v", err)
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
		Name:   "selkies",
		Exec:   "python3 %(ENV_HOME)s/.local/bin/selkies-capture-server",
		Env:    map[string]string{"SELKIES_DATA": "$HOME/.config/selkies"},
		Scope:  "user",
		Enable: true,
	}
	rendered, err := renderService(entry, testSystemdInitDef(), spec.ServiceRenderContext{
		Candy:       "selkies",
		Home:        spec.HomeToken,
		UserUnitDir: spec.HomeToken + "/.config/systemd/user",
	})
	if err != nil {
		t.Fatalf("renderService: %v", err)
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
	plan := &spec.InstallPlan{Steps: []spec.InstallStep{
		&spec.ServiceCustomStep{Name: "charly-selkies-selkies", UnitText: rendered.UnitText, UnitPath: rendered.UnitPath, TargetScope: spec.ScopeUser},
	}}
	spec.ResolveHome(plan, "/home/cachy")
	cs := plan.Steps[0].(*spec.ServiceCustomStep)
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
		Scope:       "system",
		Overrides: &spec.CandyServiceOverrides{
			Env: map[string]string{"PGDATA": "/var/lib/postgresql/data"},
		},
	}
	rendered, err := renderService(entry, testSystemdInitDef(), spec.ServiceRenderContext{
		Candy:         "postgresql",
		SystemUnitDir: "/etc/systemd/system",
	})
	if err != nil {
		t.Fatalf("renderService: %v", err)
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
	_, err := renderService(entry, testSupervisordInitDef(), spec.ServiceRenderContext{Candy: "pg"})
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
	rendered, err := renderService(entry, testSupervisordInitDef(), spec.ServiceRenderContext{Candy: "ollama"})
	if err != nil {
		t.Fatalf("renderService: %v", err)
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
		Name:   "x",
		Exec:   "/bin/true",
		Scope:  "user",
		Enable: true,
	}
	rendered, err := renderService(entry, testSystemdInitDef(), spec.ServiceRenderContext{
		Candy:       "x",
		UserUnitDir: "/home/user/.config/systemd/user",
	})
	if err != nil {
		t.Fatalf("renderService: %v", err)
	}
	if !strings.Contains(rendered.UnitText, "WantedBy=default.target") {
		t.Errorf("user-scope unit should WantedBy=default.target; got:\n%s", rendered.UnitText)
	}
	want := "/home/user/.config/systemd/user/charly-x-x.service"
	if rendered.UnitPath != want {
		t.Errorf("UnitPath = %q, want %q", rendered.UnitPath, want)
	}
}
