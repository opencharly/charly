// resource_kind_set_test.go — the resource-kind membership `charly` classifies with must come
// from the CONTRACT at consult time, not from a snapshot taken at package init.
//
// Before this test the core derived its own copy once, at init:
//
//	resourceKindSet = setFromSlice(spec.ResourceKinds)   // reserved_registry.go:46
//
// and two live predicates read it — `isExternalDeploySubstrate` (plugin_prescan.go) and
// `unresolvedDeployTargetError` (unified_targets.go). The observable is DIVERGENCE: this test
// moves the contract's own vocabulary and asks a predicate whose answer depends on membership.
//
// `unresolvedDeployTargetError` is the right probe because it distinguishes the two outcomes by
// wording, not by a returned token: a word the vocabulary declares but no corpus plugin serves is
// "a known substrate but no plugin … serves deploy:<word>", while a word nobody declares is an
// unknown-target typo. A snapshot taken at init reports the typo for a word the schema now
// declares.
package main

import (
	"strings"
	"testing"

	"github.com/opencharly/spec/spec"
)

func TestDeployTargetClassificationTracksTheVocabulary(t *testing.T) {
	const probe = "resourcekindtest"
	orig := spec.ResourceKinds
	spec.ResourceKinds = append(append([]string{}, orig...), probe)
	t.Cleanup(func() { spec.ResourceKinds = orig })

	err := unresolvedDeployTargetError("probe-deploy", probe)
	if err == nil {
		t.Fatal("unresolvedDeployTargetError returned nil for an unresolvable target")
	}
	if !strings.Contains(err.Error(), "is a known substrate") {
		t.Fatalf("the target classification does not see a word the composed schema now declares: "+
			"got %q, want the 'known substrate' branch; the core is answering from a snapshot taken "+
			"at package init, so its verdict can disagree with spec.ResourceKindSet()", err)
	}

	// The control: a word nobody declares must stay OUT, or the assertion above would pass on a
	// predicate that simply called every target known.
	err = unresolvedDeployTargetError("probe-deploy", "definitely-not-a-kind-word")
	if err == nil {
		t.Fatal("unresolvedDeployTargetError returned nil for an unknown target")
	}
	if strings.Contains(err.Error(), "is a known substrate") {
		t.Fatalf("an undeclared word classified as a known substrate: %v", err)
	}
}
