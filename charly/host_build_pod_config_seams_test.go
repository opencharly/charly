package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// TestHostBuildPodConfigResolveRef_PrefersPersistedOverlay guards the add_candy-on-pod
// deploy-resolution fix (relocated from the deleted resolved_image_test.go's
// TestResolveDeployRefPrefersPersistedOverlay, ported 1:1 onto the P13-KERNEL direction-flip's
// host-side seam handler — hostBuildPodConfigResolveRef now carries this exact logic verbatim):
// PrepareVenue persists the concrete overlay ref (BundleNode.ResolvedImage) and the resolve-ref
// seam must deploy THAT exact overlay instead of re-resolving the base image short-name (which a
// CalVer sort lets the base win on a same-minute build, deploying the overlay-less base image).
// FAILS without the fix (imageRef falls back to the base-name resolution).
func TestHostBuildPodConfigResolveRef_PrefersPersistedOverlay(t *testing.T) {
	const overlayRef = "check-addcandy-pod-overlay:abc123"

	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "charly.yml")
	if err := os.WriteFile(cfgPath, []byte("version: "+LatestSchemaVersion().String()+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	origPath := DeployConfigPath
	DeployConfigPath = func() (string, error) { return cfgPath, nil }
	t.Cleanup(func() { DeployConfigPath = origPath })
	// deploykit.SaveDeployState / LoadDeployConfigForRead below resolve the per-host
	// overlay path INDEPENDENTLY via kit.DefaultDeployConfigPath (honoring ONLY
	// kit.DeployConfigEnv, never charly's own DeployConfigPath var — see
	// sdk/kit/deployconfig.go's "shared by charly core... and candy/plugin-migrate,
	// ONE definition" note) — without this, they fall through to the operator's REAL
	// ~/.config/charly/charly.yml, leaking host state into the test (RCA 2026-07-20,
	// surfaced by the candy-level libvirt: field schema-version bump).
	t.Setenv(kit.DeployConfigEnv, cfgPath)

	// Persist the overlay ref exactly as PrepareVenue does.
	deploykit.SaveDeployState("check-addcandy-pod", "", deploykit.SaveDeployStateInput{
		Box:           "check-pod",
		Target:        "pod",
		ResolvedImage: overlayRef,
	}, marshalDeployNode)

	// Round-trip: resolveDeployResolvedImage reads it back from the per-host config.
	if got := resolveDeployResolvedImage("check-addcandy-pod", ""); got != overlayRef {
		t.Fatalf("resolveDeployResolvedImage = %q, want %q (resolved_image persist/read round-trip broken)", got, overlayRef)
	}

	// hostBuildPodConfigResolveRef prefers the persisted overlay over the base-name resolution.
	origExists := kit.LocalImageExists
	kit.LocalImageExists = func(_, ref string) bool { return ref == overlayRef }
	t.Cleanup(func() { kit.LocalImageExists = origExists })

	rep, err := hostBuildPodConfigResolveRef(t.Context(), spec.PodConfigResolveRefRequest{Box: "check-addcandy-pod"}, buildEngineContext{})
	if err != nil {
		t.Fatalf("hostBuildPodConfigResolveRef: %v", err)
	}
	if rep.ImageRef != overlayRef {
		t.Fatalf("ImageRef = %q, want %q — the base-name CalVer resolution was used instead of the persisted overlay ref", rep.ImageRef, overlayRef)
	}
}
