package main

import (
	"testing"
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

// resolveNodeOverlays moved to candy/plugin-bundle/node_resolve.go (W4
// pure-helpers relocation); TestResolveNodeOverlays_PropagatesExplicitTagToNodeVersion
// moved with it — see candy/plugin-bundle/node_resolve_test.go.
