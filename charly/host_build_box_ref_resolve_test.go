package main

import (
	"strings"
	"testing"
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
//
// The buildableShortNameForEnsure tests that used to live here are DELETED (#55 coneK1 #8): the
// host buildableShortNameForEnsure is gone (the build:ensure word resolves the build-fallback
// short name plugin-side now — buildableShortNamePlugin in candy/plugin-build/ensure.go).
func TestResolveImageRefForEnsure_ShortNameRequiresCfg(t *testing.T) {
	_, err := resolveImageRefForEnsure("check-target", nil, "")
	if err == nil {
		t.Fatal("expected error for short name with nil cfg")
	}
	if !strings.Contains(err.Error(), "charly.yml") {
		t.Errorf("error should mention charly.yml, got: %v", err)
	}
}
