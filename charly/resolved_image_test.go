package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestResolveDeployRefPrefersPersistedOverlay guards the add_candy-on-pod
// deploy-resolution fix: PrepareVenue persists the concrete overlay ref
// (BundleNode.ResolvedImage) and resolveDeployRef must deploy THAT exact overlay
// instead of re-resolving the base image: short-name (which a CalVer sort lets
// the base win on a same-minute build, deploying the overlay-less base image).
// It exercises the real persist→read round-trip through saveDeployState +
// resolveDeployResolvedImage (validating the new resolved_image wire field), then
// the resolveDeployRef preference. FAILS without the fix (imageRef falls back to
// the base-name resolution).
func TestResolveDeployRefPrefersPersistedOverlay(t *testing.T) {
	const overlayRef = "check-addcandy-pod-overlay:abc123"

	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "charly.yml")
	if err := os.WriteFile(cfgPath, []byte("version: "+LatestSchemaVersion().String()+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	origPath := DeployConfigPath
	DeployConfigPath = func() (string, error) { return cfgPath, nil }
	t.Cleanup(func() { DeployConfigPath = origPath })

	// Persist the overlay ref exactly as PrepareVenue does.
	saveDeployState("check-addcandy-pod", "", SaveDeployStateInput{
		Box:           "check-pod",
		Target:        "pod",
		ResolvedImage: overlayRef,
	})

	// Round-trip: the new helper reads it back from the per-host config.
	if got := resolveDeployResolvedImage("check-addcandy-pod", ""); got != overlayRef {
		t.Fatalf("resolveDeployResolvedImage = %q, want %q (resolved_image persist/read round-trip broken)", got, overlayRef)
	}

	// resolveDeployRef prefers the persisted overlay over the base-name resolution.
	origExists := LocalImageExists
	LocalImageExists = func(_, ref string) bool { return ref == overlayRef }
	t.Cleanup(func() { LocalImageExists = origExists })

	c := &BoxConfigSetupCmd{Box: "check-addcandy-pod"}
	_, imageRef := c.resolveDeployRef()
	if imageRef != overlayRef {
		t.Fatalf("resolveDeployRef imageRef = %q, want %q — the base-name CalVer resolution was used instead of the persisted overlay ref", imageRef, overlayRef)
	}
}
