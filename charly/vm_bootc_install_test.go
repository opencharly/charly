package main

import (
	"strings"
	"testing"

	"github.com/opencharly/sdk/kit"
)

// TestResolveBootcImageRef_FullRefPassthrough proves a full OCI ref (one
// containing "/") is returned unchanged — bootc may pull it from a registry, so
// it is neither rewritten nor required to exist in local storage. Covers both a
// tagged ref and a digest-pinned ref.
func TestResolveBootcImageRef_FullRefPassthrough(t *testing.T) {
	for _, ref := range []string{
		"quay.io/fedora/fedora-bootc:43",
		"quay.io/fedora/fedora-bootc:43@sha256:3a6b31238244f72a531a64f5fa0c102fcc1c64afcf0277f09fe85a8d6b0256d1",
	} {
		got, err := resolveBootcImageRef("podman", ref)
		if err != nil {
			t.Fatalf("resolveBootcImageRef(%q) unexpected error: %v", ref, err)
		}
		if got != ref {
			t.Errorf("resolveBootcImageRef(%q) = %q, want passthrough unchanged", ref, got)
		}
	}
}

// TestResolveBootcImageRef_ShortNameResolvesToCalVer proves an internal
// kind:image short name resolves to its newest local CalVer tag — and crucially
// NEVER to a `:latest` tag. The pre-fix code emitted
// `ghcr.io/opencharly/<name>:latest`, a ref that charly never builds or pushes
// (charly is CalVer-only), so bootc would fail to find it deep inside the
// privileged container.
func TestResolveBootcImageRef_ShortNameResolvesToCalVer(t *testing.T) {
	withLocalImages(t, []kit.LocalImageInfo{
		{
			Names:  []string{"ghcr.io/opencharly/fedora-bootc:2026.145.0900"},
			Labels: map[string]string{LabelBox: "fedora-bootc", LabelVersion: "2026.145.0900"},
		},
	})
	got, err := resolveBootcImageRef("podman", "fedora-bootc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "ghcr.io/opencharly/fedora-bootc:2026.145.0900" {
		t.Errorf("resolveBootcImageRef = %q, want the CalVer-tagged ref", got)
	}
	if strings.Contains(got, ":latest") {
		t.Errorf("resolveBootcImageRef returned a :latest ref %q — charly is CalVer-only", got)
	}
}

// TestResolveBootcImageRef_ShortNameNotBuilt proves a short name with no
// matching local image yields an actionable error pointing at `charly box build`,
// instead of silently fabricating a `:latest` ref that bootc would then fail to
// pull.
func TestResolveBootcImageRef_ShortNameNotBuilt(t *testing.T) {
	withLocalImages(t, []kit.LocalImageInfo{
		{
			Names:  []string{"ghcr.io/opencharly/something-else:2026.145.0900"},
			Labels: map[string]string{LabelBox: "something-else"},
		},
	})
	_, err := resolveBootcImageRef("podman", "fedora-bootc")
	if err == nil {
		t.Fatal("expected error for unbuilt bootc image, got nil")
	}
	if !strings.Contains(err.Error(), "charly box build fedora-bootc") {
		t.Errorf("error = %q, want it to point at `charly box build fedora-bootc`", err.Error())
	}
}
