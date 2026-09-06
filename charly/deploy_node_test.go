package main

import (
	"strings"
	"testing"

	"github.com/opencharly/spec/spec"
)

// deploy_node_test.go — tests for FleetNode tree walking and
// dotted-path resolution. TestWalkPreOrder_RootThenChildren / TestWalkPostOrder_ChildrenThenRoot
// / TestResolveNodePath_* / TestSortedChildKeys_Deterministic /
// TestMergeDeployConfigsLocalCutoverFields / TestMergeDeployConfigsPreservesAllFields relocated
// to candy/plugin-fleet (#55 decoupling, Batch A) — they asserted deploykit tree/merge
// functions directly, zero charly dep.

func TestValidateDeploymentTree_RejectsDotInName(t *testing.T) {
	deploy := map[string]spec.FleetNode{
		"bad.name": {Target: "host"},
	}
	err := spec.ValidateDeploymentTree(deploy)
	if err == nil {
		t.Fatal("expected error for '.' in deployment name")
	}
	if !strings.Contains(err.Error(), "'.'") {
		t.Errorf("error should cite the reserved character, got %v", err)
	}
}

func TestHasMembers(t *testing.T) {
	empty := &spec.FleetNode{}
	if empty.HasMembers() {
		t.Error("empty node should not report HasMembers")
	}
	withKids := &spec.FleetNode{Member: []spec.Member{{Name: "k", Position: spec.PositionInSubstrate, Node: &spec.FleetNode{}}}}
	if !withKids.HasMembers() {
		t.Error("node with members should report HasMembers")
	}
}
