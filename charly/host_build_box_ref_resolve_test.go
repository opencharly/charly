package main

import (
	"strings"
	"testing"

	"github.com/opencharly/sdk/spec"
)

// TestResolveImageRefForEnsure_FullRef — fully-qualified registry refs pass through unchanged.
func TestResolveImageRefForEnsure_FullRef(t *testing.T) {
	ref, err := resolveImageRefForEnsure("ghcr.io/opencharly/check-target:2026.124.1253", nil, "")
	if err != nil {
		t.Fatalf("full ref err: %v", err)
	}
	if ref != "ghcr.io/opencharly/check-target:2026.124.1253" {
		t.Errorf("full ref should pass through, got %q", ref)
	}
}

// TestResolveImageRefForEnsure_ShortNameRequiresCfg — short names without a *Config error with a
// friendly message naming charly.yml. A remote (`@github.com/...`) ref is NOT exercised here — the
// build:ensure plugin (candy/plugin-build) routes those through the separate
// "remote-image-resolve" seam BEFORE ever calling this function, so resolveImageRefForEnsure no
// longer has (or needs) a remote-ref branch.
func TestResolveImageRefForEnsure_ShortNameRequiresCfg(t *testing.T) {
	_, err := resolveImageRefForEnsure("check-target", nil, "")
	if err == nil {
		t.Fatal("expected error for short name with nil cfg")
	}
	if !strings.Contains(err.Error(), "charly.yml") {
		t.Errorf("error should mention charly.yml, got: %v", err)
	}
}

// TestBuildableShortNameForEnsure_FullRefBasenameLookup — the build-fallback path for full
// registry refs reverse-resolves the basename against cfg.Box. This is what lets
// `ghcr.io/opencharly/arch-builder:<tag>` build locally on a host with no ghcr.io credentials.
func TestBuildableShortNameForEnsure_FullRefBasenameLookup(t *testing.T) {
	cfg := &Config{Box: boxMapOf(map[string]spec.BoxConfig{
		"arch-builder":   {},
		"fedora-builder": {},
	})}
	cases := []struct {
		image string
		want  string
	}{
		{"ghcr.io/opencharly/arch-builder:2026.122.2252", "arch-builder"},
		{"ghcr.io/opencharly/fedora-builder:latest", "fedora-builder"},
		{"localhost:5000/arch-builder:dev", "arch-builder"},
		{"arch-builder", "arch-builder"},
		{"some-unknown-image", ""},
		{"ghcr.io/owner/totally-unknown:v1", ""},
	}
	for _, c := range cases {
		got := buildableShortNameForEnsure(c.image, cfg)
		if got != c.want {
			t.Errorf("buildableShortNameForEnsure(%q) = %q, want %q", c.image, got, c.want)
		}
	}
}

// TestBuildableShortNameForEnsure_NilCfg returns "" cleanly.
func TestBuildableShortNameForEnsure_NilCfg(t *testing.T) {
	if got := buildableShortNameForEnsure("anything", nil); got != "" {
		t.Errorf("expected '' for nil cfg, got %q", got)
	}
}

// TestBuildableShortNameForEnsure_RemoteRef returns "" — remote refs use the remote project's
// charly.yml; local build is not applicable.
func TestBuildableShortNameForEnsure_RemoteRef(t *testing.T) {
	cfg := &Config{Box: boxMapOf(map[string]spec.BoxConfig{"x": {}})}
	if got := buildableShortNameForEnsure("@github.com/owner/repo/x:tag", cfg); got != "" {
		t.Errorf("expected '' for remote ref, got %q", got)
	}
}

// TestBuildableShortNameForEnsure_NamespacedRef proves ensure-image's build fallback resolves a
// QUALIFIED (namespaced) image ref directly: `fedora.fedora-builder` is buildable as-is by the
// namespace-aware build path, but the leaf lookup can never match a dotted ref — before the fix
// the fallback returned "" and a bed racing its siblings to first-build the builder image failed
// with "no buildable short-name match" (the check-builder-vm failure mode).
func TestBuildableShortNameForEnsure_NamespacedRef(t *testing.T) {
	cfg := &Config{
		Box: boxMapOf(map[string]spec.BoxConfig{"root-image": {}}),
		Namespaces: map[string]*Config{
			"fedora": {Box: boxMapOf(map[string]spec.BoxConfig{"fedora-builder": {}})},
		},
	}

	if got := buildableShortNameForEnsure("fedora.fedora-builder", cfg); got != "fedora.fedora-builder" {
		t.Fatalf("qualified ref must resolve as-is, got %q", got)
	}
	if got := buildableShortNameForEnsure("nope.missing", cfg); got != "" {
		t.Fatalf("unresolvable qualified ref must return empty, got %q", got)
	}
	// The leaf reverse-resolution path is unchanged: a full ref whose basename matches a
	// namespaced entry resolves to the qualified name.
	if got := buildableShortNameForEnsure("ghcr.io/opencharly/fedora-builder:2026.1.1", cfg); got != "fedora.fedora-builder" {
		t.Fatalf("leaf reverse-resolution must still qualify, got %q", got)
	}
	if got := buildableShortNameForEnsure("root-image", cfg); got != "root-image" {
		t.Fatalf("root short name must resolve, got %q", got)
	}
}
