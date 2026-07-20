package main

import (
	"os"
	"path/filepath"
	"testing"
)

// Tests for deploy_ref.go.

func TestResolveDeployRefLocalImage(t *testing.T) {
	dir := t.TempDir()
	// ResolveDeployRef calls LoadUnified, which accepts ONLY the unified
	// node-form `<name>: {<kind>: <scalars>}`. Fixture uses the inline
	// node-form box (version: 2026.201.2300).
	if err := os.WriteFile(filepath.Join(dir, "charly.yml"), []byte(`
version: 2026.201.2300
myimg:
  candy:
    base: fedora
`), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveDeployRef("myimg", dir)
	if err != nil {
		t.Fatalf("ResolveDeployRef: %v", err)
	}
	if got.Kind != RefKindBox || got.Source != RefSourceLocalName || got.Name != "myimg" {
		t.Errorf("unexpected parse: %+v", got)
	}
}

func TestResolveDeployRefLocalCandy(t *testing.T) {
	dir := t.TempDir()
	lyrDir := filepath.Join(dir, "candy", "ripgrep")
	if err := os.MkdirAll(lyrDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lyrDir, "charly.yml"), []byte(`
candy:
  package: [ripgrep]
`), 0644); err != nil {
		t.Fatal(err)
	}
	// Also create charly.yml so the local-name resolver has something
	// to search — but we don't add "ripgrep" to it, so it's candy-only.
	_ = os.WriteFile(filepath.Join(dir, "charly.yml"), []byte("version: 2026.201.2300\nimage: {}\n"), 0644)

	got, err := ResolveDeployRef("ripgrep", dir)
	if err != nil {
		t.Fatalf("ResolveDeployRef: %v", err)
	}
	if got.Kind != RefKindCandy || got.Source != RefSourceLocalName {
		t.Errorf("unexpected parse: %+v", got)
	}
	if got.Name != "ripgrep" {
		t.Errorf("name = %q, want ripgrep", got.Name)
	}
}

// TestResolveDeployRefCrossKindNameReuse — same name in both box: and
// candy/ is permitted (cross-kind name reuse, 2026-05 cutover). The
// primary `<ref>` resolver returns box-first; ResolveDeployRefAsCandy
// (used by `--add-layer <ref>`) returns candy-first. Each kind remains
// reachable via explicit paths.
func TestResolveDeployRefCrossKindNameReuse(t *testing.T) {
	dir := t.TempDir()
	// Inline node-form box `dup` in charly.yml + a node-form candy `dup` in
	// candy/dup/. Cross-kind reuse of the SAME name across the box: namespace
	// and a candy directory is permitted; box-first vs candy-first precedence
	// is decided by the caller, not a uniqueness error.
	if err := os.WriteFile(filepath.Join(dir, "charly.yml"), []byte(`
version: 2026.201.2300
dup:
  candy:
    base: fedora
`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "candy", "dup"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "candy", "dup", "charly.yml"), []byte(`
dup:
  candy:
    version: "2026.150.0000"
    description: dup candy
  dup-package:
    package:
      - foo
  dup-step-0:
    check: present
    command: "true"
`), 0644); err != nil {
		t.Fatal(err)
	}

	// Primary `<ref>` positional → box-first.
	got, err := ResolveDeployRef("dup", dir)
	if err != nil {
		t.Fatalf("ResolveDeployRef: %v", err)
	}
	if got.Kind != RefKindBox {
		t.Errorf("primary ref: kind = %v, want box (box-first precedence)", got.Kind)
	}

	// `--add-layer` context → candy-first.
	got, err = ResolveDeployRefAsCandy("dup", dir)
	if err != nil {
		t.Fatalf("ResolveDeployRefAsCandy: %v", err)
	}
	if got.Kind != RefKindCandy {
		t.Errorf("add-candy ref: kind = %v, want candy (candy-first precedence)", got.Kind)
	}
}

func TestResolveDeployRefUnknownName(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "charly.yml"), []byte("version: 2026.201.2300\nimage: {}\n"), 0644)
	_, err := ResolveDeployRef("nope", dir)
	if err == nil {
		t.Fatalf("expected not-found error, got nil")
	}
}

func TestResolveDeployRefLocalPathImage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "custom-image.yml")
	if err := os.WriteFile(path, []byte(`
defaults:
  registry: ghcr.io/example
box:
  foo:
    base: fedora
`), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveDeployRef(path, dir)
	if err != nil {
		t.Fatalf("ResolveDeployRef: %v", err)
	}
	if got.Kind != RefKindBox {
		t.Errorf("kind = %v, want box", got.Kind)
	}
	if got.Source != RefSourceLocalPath {
		t.Errorf("source = %v, want local-path", got.Source)
	}
}

func TestResolveDeployRefLocalPathCandy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "custom-layer.yml")
	if err := os.WriteFile(path, []byte(`
description: A layer
rpm:
  package: [bat]
`), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveDeployRef(path, dir)
	if err != nil {
		t.Fatalf("ResolveDeployRef: %v", err)
	}
	if got.Kind != RefKindCandy {
		t.Errorf("kind = %v, want candy", got.Kind)
	}
}

func TestResolveDeployRefRemoteGitHubLegacy(t *testing.T) {
	// Legacy @-prefixed form used by today's require:/candy: fields.
	ref := "@github.com/opencharly/charly/layers/ripgrep:main"
	got, err := ResolveDeployRef(ref, "")
	if err != nil {
		t.Fatalf("ResolveDeployRef: %v", err)
	}
	if got.Kind != RefKindCandy {
		t.Errorf("kind = %v, want candy", got.Kind)
	}
	if got.Source != RefSourceRemote {
		t.Errorf("source = %v, want remote", got.Source)
	}
	if got.Remote == nil {
		t.Fatalf("Remote is nil")
	}
	if got.Remote.Name != "ripgrep" {
		t.Errorf("remote name = %q", got.Remote.Name)
	}
	if got.Remote.Version != "main" {
		t.Errorf("remote version = %q", got.Remote.Version)
	}
}

// TestResolveDeployRefRemoteCandy covers the post-2026-06-rebrand `candy/<n>`
// subpath: a remote candy ref must classify as a CANDY (not a box), so
// `charly bundle add --add-layer @github.../candy/charly:vTAG` (the form a kind:check
// bed's add_candy compiles to) is accepted instead of hitting the "remote image
// refs are not supported" guard. Without the /candy/ classification this fails.
func TestResolveDeployRefRemoteCandy(t *testing.T) {
	ref := "@github.com/opencharly/charly/candy/charly:v2026.157.0427"
	got, err := ResolveDeployRefAsCandy(ref, "")
	if err != nil {
		t.Fatalf("ResolveDeployRefAsCandy: %v", err)
	}
	if got.Kind != RefKindCandy {
		t.Errorf("kind = %v, want candy (a candy/ ref is a candy)", got.Kind)
	}
	if got.Source != RefSourceRemote || got.Remote == nil || got.Remote.Name != "charly" {
		t.Errorf("got = %+v, want remote candy named charly", got)
	}
	// And a box/<n> subpath classifies as a box.
	img, err := ResolveDeployRef("@github.com/opencharly/charly/box/fedora-coder:v2026.157.0427", "")
	if err != nil {
		t.Fatalf("ResolveDeployRef(box): %v", err)
	}
	if img.Kind != RefKindBox {
		t.Errorf("box/ ref kind = %v, want box", img.Kind)
	}
}

func TestResolveDeployRefRemoteGitHubNewSyntax(t *testing.T) {
	// New plan-approved syntax: no @ prefix, @ as version separator.
	ref := "github.com/opencharly/charly/layers/ripgrep@main"
	got, err := ResolveDeployRef(ref, "")
	if err != nil {
		t.Fatalf("ResolveDeployRef: %v", err)
	}
	if got.Kind != RefKindCandy {
		t.Errorf("kind = %v, want candy", got.Kind)
	}
	if got.Remote.Version != "main" {
		t.Errorf("remote version = %q", got.Remote.Version)
	}
}

func TestResolveDeployRefRemoteImage(t *testing.T) {
	ref := "github.com/opencharly/charly/images/fedora-coder"
	got, err := ResolveDeployRef(ref, "")
	if err != nil {
		t.Fatalf("ResolveDeployRef: %v", err)
	}
	if got.Kind != RefKindBox {
		t.Errorf("kind = %v, want box", got.Kind)
	}
}

func TestResolveDeployRefRemoteBareRepo(t *testing.T) {
	// A remote repo without /layers/ or /images/ — defaults to box
	// (points at the project's charly.yml).
	ref := "github.com/opencharly/charly"
	got, err := ResolveDeployRef(ref, "")
	if err != nil {
		t.Fatalf("ResolveDeployRef: %v", err)
	}
	if got.Kind != RefKindBox {
		t.Errorf("bare repo kind = %v, want box (default)", got.Kind)
	}
}

func TestTranslateAtVersion(t *testing.T) {
	tests := map[string]string{
		"github.com/foo/bar":                 "github.com/foo/bar",
		"github.com/foo/bar/layers/x":        "github.com/foo/bar/layers/x",
		"github.com/foo/bar/layers/x@main":   "github.com/foo/bar/layers/x:main",
		"github.com/foo/bar/layers/x@v1.2.3": "github.com/foo/bar/layers/x:v1.2.3",
		"github.com/foo/bar@main/layers/x":   "github.com/foo/bar:main/layers/x",
	}
	for in, want := range tests {
		if got := translateAtVersion(in); got != want {
			t.Errorf("translateAtVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestClassifyYAMLFile(t *testing.T) {
	dir := t.TempDir()
	image := filepath.Join(dir, "img.yml")
	layer := filepath.Join(dir, "lyr.yml")
	unknown := filepath.Join(dir, "u.yml")

	_ = os.WriteFile(image, []byte("box:\n  x: {}\n"), 0644)
	_ = os.WriteFile(layer, []byte("rpm:\n  packages: [a]\n"), 0644)
	_ = os.WriteFile(unknown, []byte("foo: bar\n"), 0644)

	if k, err := classifyYAMLFile(image); err != nil || k != RefKindBox {
		t.Errorf("box file: kind=%v err=%v, want box nil", k, err)
	}
	if k, err := classifyYAMLFile(layer); err != nil || k != RefKindCandy {
		t.Errorf("candy file: kind=%v err=%v, want candy nil", k, err)
	}
	if _, err := classifyYAMLFile(unknown); err == nil {
		t.Errorf("unknown file should return error")
	}
}
