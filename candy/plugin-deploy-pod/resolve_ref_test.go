package deploypod

import (
	"testing"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
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

// TestQualifyImageRef is the regression witness for the image-crossing defect pair. Each case
// FAILS against the pre-fix rule (which compared the box NAME to a ref and, on the inevitable
// mismatch, re-resolved through the deploy-key API keyed on the base box name).
//
// kit.ResolveShellImageRef is stubbed so the qualify leg is observable without touching local
// podman storage; a nil stub call means the ref was passed through untouched.
func TestQualifyImageRef(t *testing.T) {
	const (
		registry   = "ghcr.io/opencharly"
		boxName    = "check-pod"
		overlayRef = "check-addcandy-pod-overlay:abc123"
	)
	origResolve := kit.ResolveShellImageRef
	t.Cleanup(func() { kit.ResolveShellImageRef = origResolve })
	var qualified bool
	kit.ResolveShellImageRef = func(reg, name, tag string) string {
		qualified = true
		if reg == "" {
			// The pre-fix path dropped the registry (it re-resolved via the deploy-key API,
			// which composes a registry-less ref) — the qualification's whole point is that
			// the registry is carried.
			t.Errorf("qualification dropped the registry (name=%q tag=%q)", name, tag)
		}
		if tag == "" {
			return reg + "/" + name
		}
		return reg + "/" + name + ":" + tag
	}

	cases := []struct {
		name                                             string
		imageRef, registry, explicitRef, resolvedOverlay string
		tag                                              string
		want                                             string
		wantQualified                                    bool
	}{
		{
			// THE DEFECT: the deploy's own persisted add_candy overlay must survive untouched.
			// Pre-fix this fell through to a base-box re-resolve and deployed another image.
			name:     "persisted overlay is preserved, never qualified",
			imageRef: overlayRef, registry: registry, resolvedOverlay: overlayRef,
			want: overlayRef, wantQualified: false,
		},
		{
			name:     "short base ref is registry-qualified",
			imageRef: "check-pod", registry: registry, resolvedOverlay: "",
			want: registry + "/check-pod", wantQualified: true,
		},
		{
			name:     "short base ref keeps the requested tag through qualification",
			imageRef: "check-pod:2026.216.2119", registry: registry, resolvedOverlay: "", tag: "2026.216.2119",
			want: registry + "/check-pod:2026.216.2119", wantQualified: true,
		},
		{
			// A DIFFERENT deploy's overlay must NOT suppress qualification — the comparison is
			// against THIS deploy key's persisted overlay only.
			name:     "another deploy's overlay does not match this ref",
			imageRef: "check-pod", registry: registry, resolvedOverlay: "check-other-pod-overlay:deadbeef",
			want: registry + "/check-pod", wantQualified: true,
		},
		{
			name:     "already-full ref passes through",
			imageRef: registry + "/check-pod:2026.216.2119", registry: registry,
			want: registry + "/check-pod:2026.216.2119", wantQualified: false,
		},
		{
			name:     "Pattern B explicit ref is never recomposed",
			imageRef: "check-pod", registry: registry, explicitRef: "ghcr.io/acme/pinned:2026.001.0001",
			want: "check-pod", wantQualified: false,
		},
		{
			name:     "no registry on the box → nothing to qualify with",
			imageRef: "check-pod", registry: "",
			want: "check-pod", wantQualified: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			qualified = false
			got := qualifyImageRef(tc.imageRef, tc.registry, tc.explicitRef, tc.resolvedOverlay, boxName, tc.tag)
			if got != tc.want {
				t.Fatalf("qualifyImageRef = %q, want %q", got, tc.want)
			}
			if qualified != tc.wantQualified {
				t.Fatalf("qualification fired = %v, want %v", qualified, tc.wantQualified)
			}
		})
	}
}
