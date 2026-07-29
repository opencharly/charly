package main

import (
	"strings"
	"testing"

	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/spec/spec"
)

// TestCollectBuilderRuntimeEnv_* were removed alongside the dead
// charly.Generator.collectBuilderRuntimeEnv wrapper (P8b render-glue remainder
// cutover): the wrapper had zero non-test callers — every real caller already
// calls deploykit.Generator.CollectBuilderRuntimeEnv directly (candy_env.go,
// render_prep.go). Their coverage moved WITH the logic: sdk/deploykit/
// builder_support_test.go carries all four tests against deploykit.Generator
// directly.

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

	conf, err := deploykit.InitRenderRelayTemplate(def, 9222, "chrome", 1)
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
