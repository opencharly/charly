package main

import (
	"context"
	"github.com/opencharly/sdk/spec"
	"strings"
	"testing"
)

// TestResolveImageRefForEnsure_RemoteRef — `@github.com/...`
// passes through unchanged; the pull path routes via
// ResolveRemoteImage at pull time.
func TestResolveImageRefForEnsure_RemoteRef(t *testing.T) {
	ref, err := resolveImageRefForEnsure("@github.com/opencharly/charly/check-target:latest", nil, "")
	if err != nil {
		t.Fatalf("remote ref err: %v", err)
	}
	if !strings.HasPrefix(ref, "@github.com/") {
		t.Errorf("remote ref should pass through, got %q", ref)
	}
}

// TestResolveImageRefForEnsure_FullRef — fully-qualified registry
// refs pass through unchanged.
func TestResolveImageRefForEnsure_FullRef(t *testing.T) {
	ref, err := resolveImageRefForEnsure("ghcr.io/opencharly/check-target:2026.124.1253", nil, "")
	if err != nil {
		t.Fatalf("full ref err: %v", err)
	}
	if ref != "ghcr.io/opencharly/check-target:2026.124.1253" {
		t.Errorf("full ref should pass through, got %q", ref)
	}
}

// TestResolveImageRefForEnsure_ShortNameRequiresCfg — short names
// without a *Config error with a friendly message naming charly.yml.
func TestResolveImageRefForEnsure_ShortNameRequiresCfg(t *testing.T) {
	_, err := resolveImageRefForEnsure("check-target", nil, "")
	if err == nil {
		t.Fatal("expected error for short name with nil cfg")
	}
	if !strings.Contains(err.Error(), "charly.yml") {
		t.Errorf("error should mention charly.yml, got: %v", err)
	}
}

// TestBuildableShortName_FullRefBasenameLookup — the build-fallback
// path for full registry refs reverse-resolves the basename against
// cfg.Box. This is what lets
// `ghcr.io/opencharly/arch-builder:<tag>` build locally on a
// host with no ghcr.io credentials.
func TestBuildableShortName_FullRefBasenameLookup(t *testing.T) {
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
		got := buildableShortName(c.image, cfg)
		if got != c.want {
			t.Errorf("buildableShortName(%q) = %q, want %q", c.image, got, c.want)
		}
	}
}

// TestBuildableShortName_NilCfg returns "" cleanly.
func TestBuildableShortName_NilCfg(t *testing.T) {
	if got := buildableShortName("anything", nil); got != "" {
		t.Errorf("expected '' for nil cfg, got %q", got)
	}
}

// TestBuildableShortName_RemoteRef returns "" — remote refs use the
// remote project's charly.yml; local build is not applicable.
func TestBuildableShortName_RemoteRef(t *testing.T) {
	cfg := &Config{Box: boxMapOf(map[string]spec.BoxConfig{"x": {}})}
	if got := buildableShortName("@github.com/owner/repo/x:tag", cfg); got != "" {
		t.Errorf("expected '' for remote ref, got %q", got)
	}
}

// TestEnsureScoreImages_NilUnifiedFile returns nil (no-op).
func TestEnsureScoreImages_NilUnifiedFile(t *testing.T) {
	if err := ensureScoreImages(context.Background(), nil, nil, ""); err != nil {
		t.Errorf("nil unified file should be a no-op, got %v", err)
	}
}

// TestEnsureScoreImages_EmptyPlan returns nil — no pod-targeted steps means no
// images to ensure.
func TestEnsureScoreImages_EmptyPlan(t *testing.T) {
	uf := &UnifiedFile{}
	if err := ensureScoreImages(context.Background(), nil, uf, ""); err != nil {
		t.Errorf("plan with no pod-targeted steps should be a no-op, got %v", err)
	}
}

// TestEnsureImagePresent_EmptyImageErrors guards against silent
// no-ops on empty input.
func TestEnsureImagePresent_EmptyImageErrors(t *testing.T) {
	err := EnsureImagePresent(context.TODO(), "", nil, "")
	if err == nil {
		t.Error("expected error on empty image identifier")
	}
}
