package main

import (
	"github.com/opencharly/sdk/spec"
	"testing"
)

// TestBuildableShortName_NamespacedRef proves ensure-image's build fallback
// resolves a QUALIFIED (namespaced) image ref directly: `fedora.fedora-builder`
// is buildable as-is by the namespace-aware build path, but the leaf lookup can
// never match a dotted ref — before the fix the fallback returned "" and a bed
// racing its siblings to first-build the builder image failed with "no
// buildable short-name match" (the check-builder-vm failure mode).
func TestBuildableShortName_NamespacedRef(t *testing.T) {
	cfg := &Config{
		Box: boxMapOf(map[string]spec.BoxConfig{"root-image": {}}),
		Namespaces: map[string]*Config{
			"fedora": {Box: boxMapOf(map[string]spec.BoxConfig{"fedora-builder": {}})},
		},
	}

	if got := buildableShortName("fedora.fedora-builder", cfg); got != "fedora.fedora-builder" {
		t.Fatalf("qualified ref must resolve as-is, got %q", got)
	}
	if got := buildableShortName("nope.missing", cfg); got != "" {
		t.Fatalf("unresolvable qualified ref must return empty, got %q", got)
	}
	// The leaf reverse-resolution path is unchanged: a full ref whose basename
	// matches a namespaced entry resolves to the qualified name.
	if got := buildableShortName("ghcr.io/opencharly/fedora-builder:2026.1.1", cfg); got != "fedora.fedora-builder" {
		t.Fatalf("leaf reverse-resolution must still qualify, got %q", got)
	}
	if got := buildableShortName("root-image", cfg); got != "root-image" {
		t.Fatalf("root short name must resolve, got %q", got)
	}
}
