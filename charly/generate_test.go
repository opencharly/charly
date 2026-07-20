package main

import (
	"strings"
	"testing"

	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/spec"
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
	img := &buildkit.ResolvedBox{
		Home: "/home/user",
		BuilderConfig: &buildkit.BuilderConfig{
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
	img := &buildkit.ResolvedBox{
		Home: "/home/user",
		BuilderConfig: &buildkit.BuilderConfig{
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
	img := &buildkit.ResolvedBox{
		Home: "/home/user",
		BuilderConfig: &buildkit.BuilderConfig{
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
	img := &buildkit.ResolvedBox{Home: "/home/user", BuilderConfig: nil}
	got := g.collectBuilderRuntimeEnv([]string{"x"}, img)
	if got != nil {
		t.Errorf("expected nil when BuilderConfig is nil, got %v", got)
	}
}

// TestGenerateInitFragments / TestGenerateRelayInitFragments were removed
// alongside the dead charly.Generator.generateInitFragments wrapper (K3,
// Bucket-1 dissolution): the wrapper had zero non-test callers — the live
// equivalent (deploykit.Generator.GenerateInitFragments) is reached directly
// by candy/plugin-deploy-pod/overlay.go on its own NewRenderGeneratorFromProject
// Generator. Their coverage moved WITH the logic: sdk/deploykit/init_test.go
// carries both tests verbatim against deploykit.Generator directly.

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
	ctx := &spec.InstallContext{
		CacheMounts: rpm.CacheMount,
		Packages:    []string{"valkey"},
		Modules:     []string{"valkey:remi-9.0"},
	}
	out, err := buildkit.RenderTemplate("rpm-test", rpm.InstallTemplate, ctx)
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
	ctx := &spec.InstallContext{
		CacheMounts: pac.CacheMount,
		Packages:    []string{"neovim", "ripgrep"},
	}
	out, err := buildkit.RenderTemplate("pac-test", pac.InstallTemplate, ctx)
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
	ctx := &spec.InstallContext{
		CacheMounts: aur.CacheMount,
		StageName:   "my-tool-aur-build",
	}
	out, err := buildkit.RenderTemplate("aur-install-test", aur.InstallTemplate, ctx)
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
