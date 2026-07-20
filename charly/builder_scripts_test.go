package main

import (
	"strings"
	"testing"

	"github.com/opencharly/sdk/deploykit"
)

// Tests for deploykit.RenderBuilderScript (relocated from core, W3) — the bash scripts that run
// inside builder containers during host/VM deploys. The scripts are config-driven: they render
// each builder's phase.install.host cell from the REAL build vocabulary (the embedded charly.yml)
// — LOADED HERE (LoadBuildConfigForBox, a genuine core/loader dependency), so this fixture-builder
// stays in charly even though the render function itself moved.

// builderStepWithDef returns a BuilderStep carrying the resolved BuilderDef for
// `name` loaded from the project's real charly.yml (plus the embedded default
// build vocabulary), so deploykit.RenderBuilderScript renders the actual
// phase.install.host cell.
func builderStepWithDef(t *testing.T, name string, raw map[string]any) *deploykit.BuilderStep {
	t.Helper()
	_, bc, _, err := LoadBuildConfigForBox(repoRootDir(t))
	if err != nil {
		t.Fatalf("LoadBuildConfigForBox: %v", err)
	}
	bDef := bc.Builder[name]
	if bDef == nil {
		t.Fatalf("builder %q not defined in charly.yml", name)
	}
	return &deploykit.BuilderStep{Builder: name, CandyName: "test-layer", BuilderDef: bDef, RawStageContext: raw}
}

func TestRenderPixiScript(t *testing.T) {
	s := builderStepWithDef(t, "pixi", nil)
	out, err := deploykit.RenderBuilderScript(s, "/home/user")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	mustContain := []string{
		"set -e",
		`cd "$HOME"`,
		"pixi install",
		"system-requirements",
		`cp /work/$manifest $manifest`,
	}
	for _, m := range mustContain {
		if !strings.Contains(out, m) {
			t.Errorf("missing %q in pixi script:\n%s", m, out)
		}
	}
}

func TestRenderNpmScript(t *testing.T) {
	s := builderStepWithDef(t, "npm", nil)
	out, err := deploykit.RenderBuilderScript(s, "/home/user")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(out, "npm install -g") {
		t.Errorf("missing npm install -g: %s", out)
	}
	if !strings.Contains(out, "package.json") {
		t.Errorf("missing package.json handling: %s", out)
	}
}

func TestRenderCargoScript(t *testing.T) {
	s := builderStepWithDef(t, "cargo", nil)
	out, err := deploykit.RenderBuilderScript(s, "/home/user")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(out, `cargo install --path /work --root "$CARGO_HOME"`) {
		t.Errorf("missing cargo install line: %s", out)
	}
}

func TestRenderAurScriptPackages(t *testing.T) {
	s := builderStepWithDef(t, "aur", map[string]any{
		"packages": []string{"some-pkg", "another-pkg"},
	})
	out, err := deploykit.RenderBuilderScript(s, "/home/user")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	mustContain := []string{
		"yay -S --noconfirm --needed",
		"some-pkg",
		"another-pkg",
		"/tmp/aur-pkgs",
		"*.pkg.tar.zst",
		// The DB refresh that keeps the (cached, stale) builder DB from
		// resolving a makedepend to a mirror-rotated version (the go-1.26.3
		// .sig 404). Mirrors the aur builder's OpResolve stage (kit.BuilderResolve, R3).
		"pacman -Syu --noconfirm",
	}
	for _, m := range mustContain {
		if !strings.Contains(out, m) {
			t.Errorf("aur script missing %q:\n%s", m, out)
		}
	}
	// The refresh MUST precede yay's makedepend resolution, or it's useless.
	if syncIdx, yayIdx := strings.Index(out, "pacman -Syu --noconfirm"), strings.Index(out, "yay -S"); syncIdx < 0 || yayIdx < 0 || syncIdx > yayIdx {
		t.Errorf("pacman -Syu must come BEFORE yay -S (sync=%d yay=%d):\n%s", syncIdx, yayIdx, out)
	}
}

func TestRenderBuilderScriptUnknownBuilder(t *testing.T) {
	// A BuilderStep with no resolved BuilderDef (synthetic / unknown builder)
	// has no host cell to render → error.
	s := &deploykit.BuilderStep{Builder: "nonexistent"}
	if _, err := deploykit.RenderBuilderScript(s, "/home/user"); err == nil {
		t.Fatalf("expected error for builder with no BuilderDef")
	}
}
