package main

import (
	"strings"
	"testing"

	"github.com/opencharly/spec/spec"
)

// deploy_node_test.go — tests for BundleNode tree walking and
// dotted-path resolution. TestWalkPreOrder_RootThenChildren / TestWalkPostOrder_ChildrenThenRoot
// / TestResolveNodePath_* / TestSortedChildKeys_Deterministic /
// TestMergeDeployConfigsLocalCutoverFields / TestMergeDeployConfigsPreservesAllFields relocated
// to candy/plugin-bundle (#55 decoupling, Batch A) — they asserted deploykit tree/merge
// functions directly, zero charly dep.

func TestValidateDeploymentTree_RejectsDotInName(t *testing.T) {
	deploy := map[string]spec.BundleNode{
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

func TestHasChildren(t *testing.T) {
	empty := &spec.BundleNode{}
	if empty.HasChildren() {
		t.Error("empty node should not report HasChildren")
	}
	withKids := &spec.BundleNode{Children: map[string]*spec.BundleNode{"k": {}}}
	if !withKids.HasChildren() {
		t.Error("node with children should report HasChildren")
	}
}
