package main

import (
	"testing"

	"github.com/opencharly/spec/spec"
)

// TestProviderClassesParity (F4.2) proves the kernel's closed provider-class set is DERIVED
// from spec.ProviderClasses — the ONE CUE-owned vocabulary — and that the typed constants
// (the kernel's Go API) each appear in it (the bijection gate). A class added to
// spec/schema/candy.cue's #ProviderClassNames automatically enters this set; a class that
// stops being CUE-declared drops out and this test fails loudly if a constant still claims it.
func TestProviderClassesParity(t *testing.T) {
	// The derived closed set must be exactly the CUE vocabulary.
	if len(providerClasses) != len(spec.ProviderClasses) {
		t.Fatalf("providerClasses has %d entries, spec.ProviderClasses has %d — they must match (one CUE source, F4.2)",
			len(providerClasses), len(spec.ProviderClasses))
	}
	for _, c := range spec.ProviderClasses {
		if !providerClasses[ProviderClass(c)] {
			t.Errorf("spec.ProviderClasses contains %q but the kernel closed set does not — the map is not derived", c)
		}
	}
	// Every typed constant must remain a valid class (nothing hand-deleted from the set).
	all := []ProviderClass{ClassKind, ClassVerb, ClassDeployTarget, ClassStep, ClassBuilder,
		ClassCommand, ClassBuild, ClassLoader, ClassRefs, ClassAgentRuntime, ClassTerminal}
	for _, c := range all {
		if !providerClasses[c] {
			t.Errorf("typed constant %q is missing from the derived closed set", c)
		}
	}
}
