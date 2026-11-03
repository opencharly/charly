package main

import (
	"context"
	"testing"

	"github.com/opencharly/spec/spec"
)

// build_overlay_namespaced_test.go — the charly#968 regression: the pod-overlay prep must name
// the IMAGE the box build produced, not the box ADDRESS.
//
// A box's ADDRESS may be namespace-qualified (`ns.box`), but its image REPOSITORY is built from
// the box's LEAF name under the box/namespace registry. For an unqualified box the two coincide,
// which is why the old `base + ":" + tag` composition worked by accident; for a namespaced box it
// hands podman the address as a short name, which it cannot resolve:
//
//	short-name "charly.check-k8s-deploy-app" did not resolve to an alias ...
//
// This test drives the REAL host-build seam (hostBuildOverlay), so a regression in the ref
// composition fails here rather than only downstream in a disposable bed.

// namespaceBoxFixture writes a project whose only box lives in an imported namespace, carrying
// the namespace's OWN defaults.registry — the shape the umbrella's import of the charly repo has.
func namespaceBoxFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFixture(t, root, "charly.yml", `import:
  - ns: ./ns.yml
`)
	writeFixture(t, root, "ns.yml", `defaults:
  registry: ghcr.io/example
mybox:
  candy:
    base: quay.io/fedora/fedora:43
    build: [rpm]
    distro: [fedora]
`)
	return root
}

// TestHostBuildOverlay_NamespacedBoxNamesTheBuiltImage pins the reply's BaseImage for a
// namespaced box. On the unfixed tree the reply is "ns.mybox:<tag>" (the box ADDRESS); the box
// build produced "ghcr.io/example/mybox:<tag>".
func TestHostBuildOverlay_NamespacedBoxNamesTheBuiltImage(t *testing.T) {
	root := namespaceBoxFixture(t)
	const tag = "check-bed-2026.307.2144"
	reply, err := hostBuildOverlay(context.Background(), spec.OverlayBuildRequest{
		Dir:     root,
		Image:   "ns.mybox",
		Version: tag,
	}, buildEngineContext{})
	if err != nil {
		t.Fatalf("hostBuildOverlay: %v", err)
	}
	want := "ghcr.io/example/mybox:" + tag
	if reply.BaseImage != want {
		t.Fatalf("reply.BaseImage = %q, want %q — the overlay must name the image the box build produced, not the box address", reply.BaseImage, want)
	}
}
