package main

import (
	"strings"
	"testing"
)

// TestUnresolvedDeployTargetError distinguishes an UNKNOWN target word (a typo) from a KNOWN
// substrate whose out-of-process provider is merely not connected — the conflation that misdirected
// the check-k3s-vm RCA (both used to read "unknown target %q").
//
// The ref-based-del discriminator tests (TestPodDeploymentArtifactExists / TestResolveDelNode)
// moved to candy/plugin-fleet/del_resolve_test.go with the del resolution (K-wave 2 cone R2 bank C).
func TestUnresolvedDeployTargetError(t *testing.T) {
	// A known substrate word whose provider isn't connected → the not-connected text.
	known := unresolvedDeployTargetError("my-vm", "vm").Error()
	if !strings.Contains(known, "known substrate") || !strings.Contains(known, "not connected") {
		t.Fatalf("a known substrate must report a not-connected provider, got: %s", known)
	}
	if strings.Contains(known, "unknown target") {
		t.Fatalf("a known substrate must NOT be reported as an unknown target, got: %s", known)
	}

	// A genuinely unknown word → the unknown-target text.
	unknown := unresolvedDeployTargetError("my-thing", "poddd").Error()
	if !strings.Contains(unknown, "unknown target") {
		t.Fatalf("a typo target must report unknown target, got: %s", unknown)
	}
	if strings.Contains(unknown, "known substrate") {
		t.Fatalf("a typo target must NOT be reported as a known substrate, got: %s", unknown)
	}
}
