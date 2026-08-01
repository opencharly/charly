package deploypod

import (
	"testing"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/spec/spec"
)

// TestResolvedOverlayImage guards the add_candy-on-pod deploy-resolution behavior the former
// core TestHostBuildPodConfigResolveRef_PrefersPersistedOverlay covered (relocated here with the
// #55 Cone A Unit 2 "pod-config-resolve-ref" seam-collapse): PrepareVenue persists the concrete
// overlay ref (BundleNode.ResolvedImage), and resolveDeployRefLocal must deploy THAT exact overlay
// (gated on it existing locally) instead of re-resolving the base image short-name (which a CalVer
// sort lets the base win on a same-minute build). resolvedOverlayImage is the pure extractor; the
// full base-name-vs-overlay preference in resolveDeployRefLocal (loadDeploy seam + LocalImageExists
// gate) is exercised live by the check-pod-overlay bed's R10.
func TestResolvedOverlayImage(t *testing.T) {
	const overlayRef = "check-addcandy-pod-overlay:abc123"
	cases := []struct {
		name      string
		bundle    map[string]deploykit.BundleNode
		box, inst string
		want      string
	}{
		{
			name:   "deploy-key entry wins",
			bundle: map[string]deploykit.BundleNode{spec.DeployKey("check-addcandy-pod", "work"): {ResolvedImage: overlayRef}},
			box:    "check-addcandy-pod", inst: "work", want: overlayRef,
		},
		{
			name:   "bare key (no instance)",
			bundle: map[string]deploykit.BundleNode{"check-addcandy-pod": {ResolvedImage: overlayRef}},
			box:    "check-addcandy-pod", inst: "", want: overlayRef,
		},
		{
			name:   "bare-key fallback when instance entry lacks resolved_image",
			bundle: map[string]deploykit.BundleNode{"check-addcandy-pod": {ResolvedImage: overlayRef}},
			box:    "check-addcandy-pod", inst: "work", want: overlayRef,
		},
		{
			name:   "no resolved_image → empty (base-name resolution used)",
			bundle: map[string]deploykit.BundleNode{"check-addcandy-pod": {Image: "check-pod"}},
			box:    "check-addcandy-pod", inst: "", want: "",
		},
		{
			name:   "no entry → empty",
			bundle: map[string]deploykit.BundleNode{},
			box:    "check-addcandy-pod", inst: "", want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dc := &deploykit.BundleConfig{Bundle: tc.bundle}
			if got := resolvedOverlayImage(dc, tc.box, tc.inst); got != tc.want {
				t.Fatalf("resolvedOverlayImage = %q, want %q", got, tc.want)
			}
		})
	}
	if got := resolvedOverlayImage(nil, "x", ""); got != "" {
		t.Fatalf("resolvedOverlayImage(nil) = %q, want empty", got)
	}
}

// TestResolveDeployRefLocal_ExplicitRefShortCircuit proves the explicit_ref path (set only by
// `charly bundle from-box`) short-circuits both outputs BEFORE any reverse-channel load — so a nil
// executor is safe, exactly as the former host seam's explicit-ref-wins contract required.
func TestResolveDeployRefLocal_ExplicitRefShortCircuit(t *testing.T) {
	const ref = "ghcr.io/opencharly/versa:2026.211.0000"
	box, img, err := resolveDeployRefLocal(t.Context(), nil, "versa-prod", "", "sometag", ref)
	if err != nil {
		t.Fatalf("resolveDeployRefLocal explicit-ref: %v", err)
	}
	if box != ref || img != ref {
		t.Fatalf("explicit-ref short-circuit = (%q, %q), want (%q, %q)", box, img, ref, ref)
	}
}
