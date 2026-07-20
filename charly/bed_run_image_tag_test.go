package main

import (
	"testing"

	"github.com/opencharly/sdk/spec"
)

// #75 — bed-scoped fixture image tags. These unit tests pin the two pure/host-side
// mechanisms of the fix; the k8s-preresolver node.Version honoring + the plugin's
// per-step --tag threading are integration-proven by the concurrent check-sidecar-pod
// + check-k8s-deploy bed run (the R10 gate).

// TestBedRunImageTag proves the per-RUN bed-scoped tag is <bed>-<calver> and that
// distinct beds/runs yield distinct tags (the collision-free-by-construction
// property). Would FAIL if the formula regressed to a shared/constant tag.
func TestBedRunImageTag(t *testing.T) {
	cases := []struct {
		bed, calver, want string
	}{
		{"check-sidecar-pod", "2026.195.0600", "check-sidecar-pod-2026.195.0600"},
		{"check-k8s-deploy", "2026.195.0600", "check-k8s-deploy-2026.195.0600"},
		{"check-sidecar-pod", "2026.195.0700", "check-sidecar-pod-2026.195.0700"},
		{"", "2026.195.0600", ""}, // no bed → empty (no --tag threaded)
		{"check-x", "", ""},       // no calver → empty
	}
	for _, c := range cases {
		if got := bedRunImageTag(c.bed, c.calver); got != c.want {
			t.Errorf("bedRunImageTag(%q, %q) = %q, want %q", c.bed, c.calver, got, c.want)
		}
	}
	// Two DISTINCT beds at the SAME calver must never collide on the tag string —
	// this is the whole point of #75 (the falsified pre-fix claim was that they were
	// collision-free even sharing a fixture image name).
	a := bedRunImageTag("check-sidecar-pod", "2026.195.0600")
	b := bedRunImageTag("check-k8s-deploy", "2026.195.0600")
	if a == b {
		t.Fatalf("distinct beds produced the SAME bed-scoped tag %q — the #75 collision is not prevented", a)
	}
}

// TestResolveNodeOverlays_PropagatesExplicitTagToNodeVersion proves that an
// explicit --tag (c.Tag) is written onto the in-memory node.Version when the node
// carries no authored version. This is the vehicle by which the bed-scoped tag
// reaches the external-substrate preresolvers (k8s) + the pod overlay build (both
// read node.Version). Would FAIL without the #75 `else if tag != "" { node.Version = tag }`
// propagation in resolveNodeOverlays.
func TestResolveNodeOverlays_PropagatesExplicitTagToNodeVersion(t *testing.T) {
	c := &deployAddCmd{Tag: "check-k8s-deploy-2026.195.0600"}
	node := &spec.BundleNode{Image: "check-k8s-deploy-app"}
	_, refStr, _, tag, err := c.resolveNodeOverlays("check-k8s-deploy-workload", node, nil)
	if err != nil {
		t.Fatalf("resolveNodeOverlays: unexpected error: %v", err)
	}
	if node.Version != "check-k8s-deploy-2026.195.0600" {
		t.Errorf("node.Version = %q, want the propagated --tag %q (k8s/overlay preresolvers read node.Version)", node.Version, "check-k8s-deploy-2026.195.0600")
	}
	if tag != "check-k8s-deploy-2026.195.0600" {
		t.Errorf("resolved tag = %q, want %q", tag, "check-k8s-deploy-2026.195.0600")
	}
	if refStr != "check-k8s-deploy-app" {
		t.Errorf("refStr = %q, want the node.Image %q", refStr, "check-k8s-deploy-app")
	}

	// An authored node.Version WINS over --tag (existing precedence, unchanged) — the
	// propagation must not clobber an operator-pinned version.
	c2 := &deployAddCmd{Tag: "bed-tag"}
	node2 := &spec.BundleNode{Image: "img", Version: "operator-pin"}
	if _, _, _, tag2, err := c2.resolveNodeOverlays("d", node2, nil); err != nil {
		t.Fatalf("resolveNodeOverlays (authored version): %v", err)
	} else if tag2 != "operator-pin" || node2.Version != "operator-pin" {
		t.Errorf("authored node.Version was clobbered: tag=%q node.Version=%q, want both %q", tag2, node2.Version, "operator-pin")
	}
}
