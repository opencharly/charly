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

	// A KNOWN CUE resource kind whose deploy word no corpus plugin serves → the
	// known-but-unserved text, and NEVER an empty plugin name ("the  plugin candy").
	// Every resource kind is corpus-served today, so the condition is CONSTRUCTED: delete
	// the word from BOTH seams unresolvedDeployTargetError reads — the index-derived deploy
	// set AND the generated provider-ref index — assert, and restore both. The coverage
	// stays real without depending on a vocabulary gap that no longer exists.
	delete(externalizedDeploySubstrates, "kubevirt")
	delete(pluginProviderRefs, "deploy:kubevirt")
	defer func() {
		externalizedDeploySubstrates["kubevirt"] = true
		pluginProviderRefs["deploy:kubevirt"] = "github.com/opencharly/plugin-kubevirt/candy/plugin-kubevirt"
	}()
	if !resourceKindSet["kubevirt"] || externalizedDeploySubstrates["kubevirt"] {
		t.Fatalf("precondition: kubevirt must be a resource kind absent from the deploy set")
	}
	unserved := unresolvedDeployTargetError("my-kv", "kubevirt").Error()
	if !strings.Contains(unserved, "known substrate") || !strings.Contains(unserved, "no plugin in") {
		t.Fatalf("a known-but-unserved kind must report the missing corpus plugin, got: %s", unserved)
	}
	if strings.Contains(unserved, "the  plugin candy") {
		t.Fatalf("the diagnostic must not render an empty plugin name, got: %s", unserved)
	}
}
