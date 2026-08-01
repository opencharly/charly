package vm

import (
	"strings"
	"testing"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/container"
	"github.com/opencharly/spec/spec"
)

// withLocalImages swaps container.ListLocalImages for the duration of the test — R3 duplicate of
// charly/checkrun_charly_verbs_test.go's helper of the same name (a separate Go module; a pure
// spec/container package-level var swap, zero core coupling).
//
// #55 coneB: the local-image resolution family relocated to spec/container. resolveBootcImageRef
// calls kit.ResolveLocalImageRef, whose body is container.ResolveLocalImageRef — and that body
// reads container's OWN ListLocalImages var (the kit.ListLocalImages re-export is a value-copy
// taken once at init, so reassigning the kit re-export no longer reaches the relocated callee —
// see the testability note at sdk/kit/local_image.go:13-16). Swap the var the callee actually reads.
// kit.LocalImageInfo is a type alias for container.LocalImageInfo, so the fixture literals are
// unchanged.
func withLocalImages(t *testing.T, images []kit.LocalImageInfo) {
	t.Helper()
	orig := container.ListLocalImages
	container.ListLocalImages = func(engine string) ([]container.LocalImageInfo, error) {
		return images, nil
	}
	t.Cleanup(func() { container.ListLocalImages = orig })
}

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
			Labels: map[string]string{spec.LabelBox: "fedora-bootc", spec.LabelVersion: "2026.145.0900"},
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
			Labels: map[string]string{spec.LabelBox: "something-else"},
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
