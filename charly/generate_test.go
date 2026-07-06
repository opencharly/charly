package main

import (
	"os"
	"strings"
	"testing"
)

// TestCollectBuilderRuntimeEnv_TriggeredEmitsRuntimeEnv is the
// regression for the 2026-04-29 jupyter-PATH-bug cutover. The pixi
// builder's runtime env contract (PIXI_CACHE_DIR + RATTLER_CACHE_DIR +
// ~/.pixi/{bin,envs/default/bin}) must reach any image whose candies
// have `pixi.toml` — even if `pixi` is NOT a top-level candy.
func TestCollectBuilderRuntimeEnv_TriggeredEmitsRuntimeEnv(t *testing.T) {
	g := &Generator{
		Candies: map[string]*Candy{
			"jupyter": {Name: "jupyter", HasPixiToml: true},
		},
	}
	img := &ResolvedBox{
		Home: "/home/user",
		BuilderConfig: &BuilderConfig{
			Builder: map[string]*BuilderDef{
				"pixi": {
					DetectFiles:       []string{"pixi.toml", "pyproject.toml"},
					RuntimeEnv:        map[string]string{"PIXI_CACHE_DIR": "~/.cache/pixi"},
					PathContributions: []string{"~/.pixi/bin", "~/.pixi/envs/default/bin"},
				},
			},
		},
	}

	got := g.collectBuilderRuntimeEnv([]string{"jupyter"}, img)
	if len(got) != 1 {
		t.Fatalf("got %d EnvConfigs, want 1", len(got))
	}
	cfg := got[0]
	if cfg.Vars["PIXI_CACHE_DIR"] != "~/.cache/pixi" {
		t.Errorf("Vars[PIXI_CACHE_DIR] = %q, want \"~/.cache/pixi\"", cfg.Vars["PIXI_CACHE_DIR"])
	}
	if len(cfg.PathAppend) != 2 || cfg.PathAppend[0] != "~/.pixi/bin" || cfg.PathAppend[1] != "~/.pixi/envs/default/bin" {
		t.Errorf("PathAppend = %v, want [~/.pixi/bin ~/.pixi/envs/default/bin]", cfg.PathAppend)
	}
}

// TestCollectBuilderRuntimeEnv_NotTriggered: when no candy triggers a
// builder, the builder must NOT contribute. Otherwise every image
// would inherit pixi env even when it has no Python in it.
func TestCollectBuilderRuntimeEnv_NotTriggered(t *testing.T) {
	g := &Generator{
		Candies: map[string]*Candy{
			"chrome": {Name: "chrome"}, // no pixi.toml, no pyproject.toml
		},
	}
	img := &ResolvedBox{
		Home: "/home/user",
		BuilderConfig: &BuilderConfig{
			Builder: map[string]*BuilderDef{
				"pixi": {
					DetectFiles:       []string{"pixi.toml"},
					RuntimeEnv:        map[string]string{"PIXI_CACHE_DIR": "~/.cache/pixi"},
					PathContributions: []string{"~/.pixi/envs/default/bin"},
				},
			},
		},
	}

	got := g.collectBuilderRuntimeEnv([]string{"chrome"}, img)
	if got != nil {
		t.Errorf("expected no contributions when no layer triggers builder, got %v", got)
	}
}

// TestCollectBuilderRuntimeEnv_MultipleCandies verifies that even when
// many candies trigger the same builder (a future Python-heavy image
// where every candy has its own pixi.toml), the builder is counted
// once — no duplicate ENV PATH entries.
func TestCollectBuilderRuntimeEnv_MultipleCandies(t *testing.T) {
	g := &Generator{
		Candies: map[string]*Candy{
			"a": {Name: "a", HasPixiToml: true},
			"b": {Name: "b", HasPixiToml: true},
			"c": {Name: "c", HasPixiToml: true},
		},
	}
	img := &ResolvedBox{
		Home: "/home/user",
		BuilderConfig: &BuilderConfig{
			Builder: map[string]*BuilderDef{
				"pixi": {
					DetectFiles:       []string{"pixi.toml"},
					PathContributions: []string{"~/.pixi/bin"},
				},
			},
		},
	}
	got := g.collectBuilderRuntimeEnv([]string{"a", "b", "c"}, img)
	if len(got) != 1 {
		t.Errorf("got %d EnvConfigs, want 1 (de-duped)", len(got))
	}
}

// TestCollectBuilderRuntimeEnv_NilBuilderConfig: defensive — the legacy
// path through `LoadConfig` (test mode without build.yml) leaves
// BuilderConfig nil. Don't panic.
func TestCollectBuilderRuntimeEnv_NilBuilderConfig(t *testing.T) {
	g := &Generator{Candies: map[string]*Candy{"x": {Name: "x", HasPixiToml: true}}}
	img := &ResolvedBox{Home: "/home/user", BuilderConfig: nil}
	got := g.collectBuilderRuntimeEnv([]string{"x"}, img)
	if got != nil {
		t.Errorf("expected nil when BuilderConfig is nil, got %v", got)
	}
}

func TestResolveBaseImage_InternalUseCalVer(t *testing.T) {
	g := &Generator{
		Boxes: map[string]*ResolvedBox{
			"fedora": {
				Name:           "fedora",
				Base:           "quay.io/fedora/fedora:43",
				IsExternalBase: true,
				Registry:       "ghcr.io/opencharly",
				Tag:            "2026.046.1415",
				FullTag:        "ghcr.io/opencharly/fedora:2026.046.1415",
			},
			"fedora-test": {
				Name:           "fedora-test",
				Base:           "fedora",
				IsExternalBase: false,
				Registry:       "ghcr.io/opencharly",
				Tag:            "2026.046.1415",
				FullTag:        "ghcr.io/opencharly/fedora-test:2026.046.1415",
			},
		},
	}

	// External base should return the base as-is
	got := g.resolveBaseImage(g.Boxes["fedora"])
	if got != "quay.io/fedora/fedora:43" {
		t.Errorf("resolveBaseImage(fedora) = %q, want external base", got)
	}

	// Internal base should return the parent's full CalVer tag
	got = g.resolveBaseImage(g.Boxes["fedora-test"])
	want := "ghcr.io/opencharly/fedora:2026.046.1415"
	if got != want {
		t.Errorf("resolveBaseImage(fedora-test) = %q, want %q", got, want)
	}
}

func TestGenerateTraefikRoutes(t *testing.T) {
	tmpDir := t.TempDir()

	g := &Generator{
		BuildDir: tmpDir,
		Candies: map[string]*Candy{
			"traefik": {
				Name: "traefik",
				plan: []Step{{Run: "build", Op: cmdOp("true")}},
			},
			"svc": {
				Name:  "svc",
				plan:  []Step{{Run: "build", Op: cmdOp("true")}},
				route: &RouteConfig{Host: "svc.localhost", Port: "9090"},
			},
		},
	}

	err := g.generateTraefikRoutes("test-image", []string{"traefik", "svc"}, &ResolvedBox{})
	if err != nil {
		t.Fatalf("generateTraefikRoutes() error = %v", err)
	}

	data, err := os.ReadFile(tmpDir + "/test-image/traefik-routes.yml")
	if err != nil {
		t.Fatalf("reading generated routes YAML: %v", err)
	}
	yaml := string(data)

	// Check structure
	if !strings.Contains(yaml, "http:") {
		t.Error("missing http: key")
	}
	if !strings.Contains(yaml, "routers:") {
		t.Error("missing routers: key")
	}
	if !strings.Contains(yaml, "services:") {
		t.Error("missing services: key")
	}

	// Check route entry
	if !strings.Contains(yaml, "svc:") {
		t.Error("missing svc router/service entry")
	}
	if !strings.Contains(yaml, `Host(`+"`"+`svc.localhost`+"`"+`)`) {
		t.Error("missing Host rule")
	}
	if !strings.Contains(yaml, "http://127.0.0.1:9090") {
		t.Error("missing backend URL")
	}
	if !strings.Contains(yaml, "- web") {
		t.Error("missing entryPoints web")
	}
}

func TestGenerateRouteWithoutTraefik_NoTraefikRoutes(t *testing.T) {
	// When an image has route candies but no traefik candy,
	// traefik-routes.yml should NOT be generated
	tmpDir := t.TempDir()

	g := &Generator{
		BuildDir: tmpDir,
		Config:   &Config{},
		Candies: map[string]*Candy{
			"svc": {
				Name:  "svc",
				plan:  []Step{{Run: "build", Op: cmdOp("true")}},
				route: &RouteConfig{Host: "svc.localhost", Port: "9090"},
			},
		},
		Boxes: map[string]*ResolvedBox{
			"test-image": {
				Name:           "test-image",
				Base:           "quay.io/fedora/fedora:43",
				IsExternalBase: true,
				Registry:       "ghcr.io/test",
				Tag:            "latest",
				FullTag:        "ghcr.io/test/test-box:latest",
				Candy:          []string{"svc"},
				Pkg:            "rpm",
				BuildFormats:   []string{"rpm"},
				Tags:           []string{"all", "rpm"},
				User:           "user",
				UID:            1000,
				GID:            1000,
				Home:           "/home/user",
			},
		},
		Containerfiles: make(map[string]string),
	}

	err := g.generateContainerfile("test-image")
	if err != nil {
		t.Fatalf("generateContainerfile() error = %v", err)
	}

	// traefik-routes.yml should NOT exist
	_, err = os.ReadFile(tmpDir + "/test-image/traefik-routes.yml")
	if err == nil {
		t.Error("traefik-routes.yml should NOT be generated when traefik layer is absent")
	}

	// Containerfile should NOT reference traefik-routes
	content := g.Containerfiles["test-image"]
	if strings.Contains(content, "traefik-routes") {
		t.Error("Containerfile should not reference traefik-routes when traefik layer is absent")
	}
}

func TestGenerateInitFragments(t *testing.T) {
	tmpDir := t.TempDir()

	// Schema-driven: each candy's service: list contains structured entries.
	// generateInitFragments iterates them and calls RenderService per entry.
	g := &Generator{
		BuildDir: tmpDir,
		Candies: map[string]*Candy{
			"python": {
				Name: "python",
				plan: []Step{{Run: "build", Op: cmdOp("true")}},
			},
			"svc": {
				Name:        "svc",
				InitSystems: map[string]bool{"supervisord": true},
				plan:        []Step{{Run: "build", Op: cmdOp("true")}},
				service: []ServiceEntry{
					{Name: "svc", Exec: "svc serve"},
				},
			},
			"other": {
				Name:        "other",
				InitSystems: map[string]bool{"supervisord": true},
				plan:        []Step{{Run: "build", Op: cmdOp("true")}},
				service: []ServiceEntry{
					{Name: "other", Exec: "other run"},
				},
			},
		},
	}

	// Minimal supervisord-like template that renders a [program:NAME] block.
	supervisordDef := withRaw(&ResolvedInit{
		Model:       "fragment_assembly",
		FragmentDir: "supervisor",
		ServiceSchema: &ServiceSchemaDef{
			SupportsPackaged: false,
			ServiceTemplate:  "[program:{{.Name}}]\ncommand={{.Exec}}\n",
		},
	})

	err := g.generateInitFragments("test-image", "supervisord", supervisordDef, []string{"python", "svc", "other"})
	if err != nil {
		t.Fatalf("generateInitFragments() error = %v", err)
	}

	// Candy ordering: python=1, svc=2, other=3. Each candy with service entries
	// gets ONE fragment file named <NN>-<candy>.conf containing all its entries.
	data, err := os.ReadFile(tmpDir + "/test-image/supervisor/02-svc.conf")
	if err != nil {
		t.Fatalf("reading svc supervisor fragment: %v", err)
	}
	if !strings.Contains(string(data), "[program:svc]") {
		t.Errorf("svc fragment missing [program:svc]; got: %q", string(data))
	}
	if !strings.Contains(string(data), "command=svc serve") {
		t.Errorf("svc fragment missing exec command; got: %q", string(data))
	}

	data, err = os.ReadFile(tmpDir + "/test-image/supervisor/03-other.conf")
	if err != nil {
		t.Fatalf("reading other supervisor fragment: %v", err)
	}
	if !strings.Contains(string(data), "[program:other]") {
		t.Errorf("other fragment missing [program:other]; got: %q", string(data))
	}

	// python has no service: entry → no fragment file.
	if _, err := os.Stat(tmpDir + "/test-image/supervisor/01-python.conf"); err == nil {
		t.Error("python should not produce a fragment")
	}
}

func TestGenerateRelayInitFragments(t *testing.T) {
	tmpDir := t.TempDir()

	relayTmpl := "[program:relay-{{.Port}}]\ncommand=/usr/local/bin/relay-wrapper {{.Port}}\nautostart=true\nautorestart=true\npriority=1\nstartsecs=0\nstdout_logfile=/dev/fd/1\nstdout_logfile_maxbytes=0\nredirect_stderr=true\n"

	g := &Generator{
		BuildDir: tmpDir,
		Candies: map[string]*Candy{
			"socat": {
				Name: "socat",
				plan: []Step{{Run: "build", Op: cmdOp("true")}},
			},
			"chrome": {
				Name:           "chrome",
				plan:           []Step{{Run: "build", Op: cmdOp("true")}},
				PortRelayPorts: []int{9222},
				InitSystems:    map[string]bool{"supervisord": true},
				service: []ServiceEntry{
					{Name: "chrome", Exec: "chrome"},
				},
			},
		},
	}

	supervisordDef := withRaw(&ResolvedInit{
		Model:       "fragment_assembly",
		FragmentDir: "supervisor",
		ServiceSchema: &ServiceSchemaDef{
			SupportsPackaged: false,
			ServiceTemplate:  "[program:{{.Name}}]\ncommand={{.Exec}}\n",
		},
		RelayTemplate: relayTmpl,
	})

	err := g.generateInitFragments("test-image", "supervisord", supervisordDef, []string{"socat", "chrome"})
	if err != nil {
		t.Fatalf("generateInitFragments() error = %v", err)
	}

	// Candy ordering: socat=1, chrome=2. chrome has both a service: entry
	// and a port_relay, producing 02-chrome.conf + 02-relay-9222.conf.
	data, err := os.ReadFile(tmpDir + "/test-image/supervisor/02-chrome.conf")
	if err != nil {
		t.Fatalf("reading chrome supervisor config: %v", err)
	}
	if !strings.Contains(string(data), "[program:chrome]") {
		t.Error("chrome fragment should contain [program:chrome]")
	}

	data, err = os.ReadFile(tmpDir + "/test-image/supervisor/02-relay-9222.conf")
	if err != nil {
		t.Fatalf("reading relay supervisor config: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "[program:relay-9222]") {
		t.Error("relay fragment should contain [program:relay-9222]")
	}
	if !strings.Contains(content, "relay-wrapper 9222") {
		t.Error("relay fragment should contain relay-wrapper 9222 command")
	}
	if !strings.Contains(content, "autostart=true") {
		t.Error("relay fragment should have autostart=true")
	}
	if !strings.Contains(content, "priority=1") {
		t.Error("relay fragment should have priority=1")
	}

	// socat has no supervisord or port_relay, should not have a config
	_, err = os.ReadFile(tmpDir + "/test-image/supervisor/01-socat.conf")
	if err == nil {
		t.Error("socat should not have a supervisor config")
	}
}

func TestRenderRelayTemplate(t *testing.T) {
	relayTmpl := "[program:relay-{{.Port}}]\ncommand=/usr/local/bin/relay-wrapper {{.Port}}\nautostart=true\nautorestart=true\npriority=1\nstartsecs=0\nstdout_logfile=/dev/fd/1\nstdout_logfile_maxbytes=0\nredirect_stderr=true\n"
	def := &ResolvedInit{
		RelayTemplate: relayTmpl,
	}

	conf, err := initRenderRelayTemplate(def, 9222, "chrome", 1)
	if err != nil {
		t.Fatalf("RenderRelayTemplate() error = %v", err)
	}

	if !strings.Contains(conf, "[program:relay-9222]") {
		t.Error("should contain [program:relay-9222]")
	}
	if !strings.Contains(conf, "command=/usr/local/bin/relay-wrapper 9222") {
		t.Error("should contain relay-wrapper command")
	}
	if !strings.Contains(conf, "autostart=true") {
		t.Error("should contain autostart=true")
	}
	if !strings.Contains(conf, "autorestart=true") {
		t.Error("should contain autorestart=true")
	}
	if !strings.Contains(conf, "priority=1") {
		t.Error("should contain priority=1")
	}
	if !strings.HasSuffix(conf, "\n") {
		t.Error("should end with newline")
	}
}

func TestRpmTemplateWithModules(t *testing.T) {
	fedora := testDistroDef("fedora")
	rpm := fedora.Format["rpm"]
	ctx := &InstallContext{
		CacheMounts: rpm.CacheMount,
		Packages:    []string{"valkey"},
		Modules:     []string{"valkey:remi-9.0"},
	}
	out, err := RenderTemplate("rpm-test", rpm.InstallTemplate, ctx)
	if err != nil {
		t.Fatalf("render error: %v", err)
	}

	if !strings.Contains(out, "dnf module reset -y valkey") {
		t.Error("should contain dnf module reset")
	}
	if !strings.Contains(out, "dnf module enable -y valkey:remi-9.0") {
		t.Error("should contain dnf module enable")
	}
	if !strings.Contains(out, "dnf install -y") {
		t.Error("should contain dnf install")
	}
	if !strings.Contains(out, "valkey") {
		t.Error("should contain package name")
	}
}

func TestPacTemplateBasic(t *testing.T) {
	arch := testDistroDef("arch")
	pac := arch.Format["pac"]
	ctx := &InstallContext{
		CacheMounts: pac.CacheMount,
		Packages:    []string{"neovim", "ripgrep"},
	}
	out, err := RenderTemplate("pac-test", pac.InstallTemplate, ctx)
	if err != nil {
		t.Fatalf("render error: %v", err)
	}
	if !strings.Contains(out, "pacman -Syu --noconfirm") {
		t.Error("should contain pacman -Syu --noconfirm")
	}
	if !strings.Contains(out, "neovim") {
		t.Error("should contain neovim")
	}
	if !strings.Contains(out, "/var/cache/pacman/pkg") {
		t.Error("should use pacman cache mount")
	}
}

func TestAurInstallTemplate(t *testing.T) {
	arch := testDistroDef("arch")
	aur := arch.Format["aur"]
	ctx := &InstallContext{
		CacheMounts: aur.CacheMount,
		StageName:   "my-tool-aur-build",
	}
	out, err := RenderTemplate("aur-install-test", aur.InstallTemplate, ctx)
	if err != nil {
		t.Fatalf("render error: %v", err)
	}
	if !strings.Contains(out, "COPY --from=my-tool-aur-build /tmp/aur-pkgs/") {
		t.Error("should COPY from AUR build stage")
	}
	if !strings.Contains(out, "pacman -U --noconfirm") {
		t.Error("should install with pacman -U")
	}
}

func TestBuilderRefForFormat(t *testing.T) {
	g := &Generator{
		Boxes: map[string]*ResolvedBox{
			"arch-img": {
				Builder: BuilderMap{"aur": "arch-builder", "pixi": "arch-builder"},
			},
			"arch-builder": {
				FullTag: "ghcr.io/opencharly/arch-builder:2026.084.1200",
			},
			"no-aur-img": {
				Builder: BuilderMap{},
			},
		},
	}

	ref := g.builderRefForFormat("arch-img", "aur")
	if ref != "ghcr.io/opencharly/arch-builder:2026.084.1200" {
		t.Errorf("builderRefForFormat(aur) = %q, want full tag", ref)
	}

	ref = g.builderRefForFormat("arch-img", "pixi")
	if ref != "ghcr.io/opencharly/arch-builder:2026.084.1200" {
		t.Errorf("builderRefForFormat(pixi) = %q, want full tag", ref)
	}

	ref = g.builderRefForFormat("no-aur-img", "aur")
	if ref != "" {
		t.Errorf("builderRefForFormat(aur) = %q, want empty", ref)
	}
}

// TestWriteDataStaging_RemoteCandyUsesShortStageAlias is the regression for the
// 2026-05-24 cachyos-GPU cutover: a DATA candy fetched via a remote @github ref
// is keyed in g.Candies by its FULL ref, but its `FROM scratch AS <name>` stage
// uses the SHORT name (candy.Name). The data COPY --from must reference the short
// alias, else podman fails with "no stage or image found" (it tries to pull the
// full ref as an image). Local data candies are unaffected (key == Name).
func TestWriteDataStaging_RemoteCandyUsesShortStageAlias(t *testing.T) {
	fullKey := "github.com/opencharly/charly/layers/notebook-templates"
	g := &Generator{
		Candies: map[string]*Candy{
			fullKey: {Name: "notebook-templates", data: []DataYAML{{Src: "data/notebooks", Volume: "workspace"}}},
		},
	}
	img := &ResolvedBox{UID: 1000, GID: 1000}
	var b strings.Builder
	g.writeDataStaging(&b, []string{fullKey}, img)
	out := b.String()
	if !strings.Contains(out, "COPY --from=notebook-templates ") {
		t.Errorf("data COPY must use the short stage alias; got:\n%s", out)
	}
	if strings.Contains(out, "COPY --from="+fullKey) {
		t.Errorf("data COPY used the full @github ref as the stage name (the bug); got:\n%s", out)
	}
}
