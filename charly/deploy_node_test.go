package main

import (
	"strings"
	"testing"

	"github.com/opencharly/spec/spec"
)

// deploy_node_test.go — tests for DeployNode tree walking and
// dotted-path resolution. TestWalkPreOrder_RootThenChildren / TestWalkPostOrder_ChildrenThenRoot
// / TestResolveNodePath_* / TestSortedChildKeys_Deterministic /
// TestMergeDeployConfigsLocalCutoverFields / TestMergeDeployConfigsPreservesAllFields relocated
// to candy/plugin-fleet (#55 decoupling, Batch A) — they asserted deploykit tree/merge
// functions directly, zero charly dep.

// TestValidateDeploymentTree_AcceptsDottedIdentity: the deploy-identity grammar is
// dot-LEGAL (spec#172, the unified-deploy-identity cutover). `.` joins the
// namespace/member path (`charly.check-docs`) and `/` separates the optional instance
// (`versa/ecovoyage`) — a namespaced deploy writes its dotted identity as the overlay
// key, so a well-formed dotted key MUST be accepted (the old dot-ban poisoned the whole
// overlay). A MALFORMED identity — an empty path segment (leading/trailing/doubled `.`)
// or an empty instance — stays rejected, citing the defect.
func TestValidateDeploymentTree_AcceptsDottedIdentity(t *testing.T) {
	deploy := map[string]spec.DeployNode{
		"charly.check-docs": {Target: "host"},
		"versa/ecovoyage":   {Target: "host"},
		"a.b.c/d":           {Target: "host"},
	}
	if err := spec.ValidateDeploymentTree(deploy); err != nil {
		t.Fatalf("a well-formed dotted deploy identity must be accepted, got %v", err)
	}
}

func TestValidateDeploymentTree_RejectsEmptyPathSegment(t *testing.T) {
	for _, name := range []string{".bad", "bad.", "a..b"} {
		deploy := map[string]spec.DeployNode{name: {Target: "host"}}
		err := spec.ValidateDeploymentTree(deploy)
		if err == nil {
			t.Fatalf("expected an empty-segment error for %q", name)
		}
		if !strings.Contains(err.Error(), "empty path segment") {
			t.Errorf("error should cite the empty path segment for %q, got %v", name, err)
		}
	}
}

func TestValidateDeploymentTree_RejectsEmptyInstance(t *testing.T) {
	deploy := map[string]spec.DeployNode{"versa/": {Target: "host"}}
	err := spec.ValidateDeploymentTree(deploy)
	if err == nil {
		t.Fatal("expected an empty-instance error for a trailing '/'")
	}
	if !strings.Contains(err.Error(), "empty instance segment") {
		t.Errorf("error should cite the empty instance segment, got %v", err)
	}
}

func TestHasMembers(t *testing.T) {
	empty := &spec.DeployNode{}
	if empty.HasMembers() {
		t.Error("empty node should not report HasMembers")
	}
	withKids := &spec.DeployNode{Member: []spec.Member{{Name: "k", Position: spec.PositionInSubstrate, Node: &spec.DeployNode{}}}}
	if !withKids.HasMembers() {
		t.Error("node with members should report HasMembers")
	}
}
