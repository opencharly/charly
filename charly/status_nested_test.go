package main

import (
	"testing"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// status_nested_test.go — HOST-side pre-resolution tests for
// buildStatusRootsTree/buildStatusChildNodes (status_nested.go). The PURE
// fold (claim → inherit real data → drop from top level; synthesize
// declared/nested; sorted order; dedup) moved OUT of core to
// candy/plugin-status/overlay.go, operating on the []spec.StatusNestedNode
// this file builds — its byte-parity is proven by the candy's
// overlay_golden_test.go. This file proves the HOST pre-resolution alone:
// the tree shape, the MatchKeys candidate order, Kind classification, and the
// --nested live-probe threading.

// nestedUnified builds a minimal UnifiedFile projection carrying one declared
// nested topology: a target:pod parent `check-android-emulator-pod` with two
// target:android nested children `device` and `device-net` (the
// check-android-emulator-pod shape), plus an unrelated flat pod deploy the
// tree-builder must leave alone (no root emitted for a childless entry).
func nestedUnified() *UnifiedFile {
	return &UnifiedFile{
		Bundle: map[string]spec.BundleNode{
			"check-android-emulator-pod": {
				Target: "pod",
				Image:  "android-emulator",
				Children: map[string]*spec.BundleNode{
					"device":     {Target: "android", From: "pixel9a-36", AddCandy: []string{"android-test-apps"}},
					"device-net": {Target: "android", From: "pixel9a-endpoint", AddCandy: []string{"android-apidemos"}},
				},
			},
			"some-flat-pod": {Target: "pod", Image: "redis"},
		},
	}
}

// findRootNode returns the root node with the given Key, or nil.
func findRootNode(roots []spec.StatusNestedNode, key string) *spec.StatusNestedNode {
	for i := range roots {
		if roots[i].Key == key {
			return &roots[i]
		}
	}
	return nil
}

// findChildNode returns the child node with the given Key, or nil.
func findChildNode(children []*spec.StatusNestedNode, key string) *spec.StatusNestedNode {
	for _, c := range children {
		if c != nil && c.Key == key {
			return c
		}
	}
	return nil
}

// TestBuildStatusRootsTree_ChildlessRootSkipped verifies a declared entry with
// no children (some-flat-pod) never emits a root node — the candy overlay
// skips a childless root anyway (`if !root.HasChildren() { continue }`).
func TestBuildStatusRootsTree_ChildlessRootSkipped(t *testing.T) {
	opts := CollectOpts{Unified: nestedUnified(), RunMode: "quadlet"}
	roots := buildStatusRootsTree(opts, false)
	if len(roots) != 1 {
		t.Fatalf("roots = %d, want 1 (only the entry WITH children emits a root): %+v", len(roots), roots)
	}
	if roots[0].Key != "check-android-emulator-pod" {
		t.Fatalf("root Key = %q, want %q", roots[0].Key, "check-android-emulator-pod")
	}
	if findRootNode(roots, "some-flat-pod") != nil {
		t.Errorf("childless entry some-flat-pod must not emit a root node")
	}
}

// TestBuildStatusRootsTree_DeclaredChildren verifies the root + child shape:
// root MatchKeys=[key], HasChildren=true; each android child's Kind, sorted
// order, and MatchKeys=[dottedPath, flattenedName] (in that priority order).
func TestBuildStatusRootsTree_DeclaredChildren(t *testing.T) {
	const parent = "check-android-emulator-pod"
	opts := CollectOpts{Unified: nestedUnified(), RunMode: "quadlet"}
	roots := buildStatusRootsTree(opts, false)

	root := findRootNode(roots, parent)
	if root == nil {
		t.Fatalf("root %q not found in %+v", parent, roots)
	}
	if !root.HasChildren {
		t.Errorf("root.HasChildren = false, want true")
	}
	if len(root.MatchKeys) != 1 || root.MatchKeys[0] != parent {
		t.Errorf("root.MatchKeys = %v, want [%q]", root.MatchKeys, parent)
	}
	if root.Kind != spec.SubstratePod {
		t.Errorf("root.Kind = %q, want %q", root.Kind, spec.SubstratePod)
	}
	if len(root.Children) != 2 {
		t.Fatalf("root.Children = %d, want 2: %+v", len(root.Children), root.Children)
	}

	for _, key := range []string{"device", "device-net"} {
		c := findChildNode(root.Children, key)
		if c == nil {
			t.Fatalf("child %q not found", key)
		}
		if c.Kind != spec.SubstrateAndroid {
			t.Errorf("child %q Kind = %q, want %q", key, c.Kind, spec.SubstrateAndroid)
		}
		wantPath := parent + "." + key
		wantMatchKeys := []string{wantPath, kit.NestedContainerName(wantPath)}
		if len(c.MatchKeys) != 2 || c.MatchKeys[0] != wantMatchKeys[0] || c.MatchKeys[1] != wantMatchKeys[1] {
			t.Errorf("child %q MatchKeys = %v, want %v", key, c.MatchKeys, wantMatchKeys)
		}
		if c.HasChildren {
			t.Errorf("leaf child %q HasChildren = true, want false", key)
		}
		// Default (nested=false): no live probe was run.
		if c.LiveStatus != "" {
			t.Errorf("child %q LiveStatus = %q, want empty (nested=false)", key, c.LiveStatus)
		}
	}
}

// TestBuildStatusRootsTree_MatchKeysOrderVmPod verifies the MatchKeys
// candidate order for a nested-pod-in-vm child: dotted path first, the
// flattened NestedContainerName second — the SAME priority order the former
// claimFlatRow tried them in.
func TestBuildStatusRootsTree_MatchKeysOrderVmPod(t *testing.T) {
	const parent = "stack-vm"
	uf := &UnifiedFile{
		Bundle: map[string]spec.BundleNode{
			parent: {
				Target: "vm",
				From:   "stack-vm",
				Children: map[string]*spec.BundleNode{
					"web": {Target: "pod", Image: "nginx"},
				},
			},
		},
	}
	opts := CollectOpts{Unified: uf, RunMode: "quadlet"}
	roots := buildStatusRootsTree(opts, false)

	root := findRootNode(roots, parent)
	if root == nil {
		t.Fatalf("root %q not found in %+v", parent, roots)
	}
	if root.Kind != spec.SubstrateVM {
		t.Errorf("root.Kind = %q, want %q", root.Kind, spec.SubstrateVM)
	}
	if len(root.Children) != 1 {
		t.Fatalf("root.Children = %d, want 1: %+v", len(root.Children), root.Children)
	}
	web := root.Children[0]
	if web.Key != "web" || web.Kind != spec.SubstratePod {
		t.Errorf("web child = %+v, want Key=web Kind=pod", web)
	}
	wantDotted := parent + ".web"
	wantFlat := kit.NestedContainerName(wantDotted)
	if len(web.MatchKeys) != 2 || web.MatchKeys[0] != wantDotted || web.MatchKeys[1] != wantFlat {
		t.Errorf("web.MatchKeys = %v, want [%q, %q]", web.MatchKeys, wantDotted, wantFlat)
	}
}

// TestBuildStatusRootsTree_LiveProbeUnreachable verifies the --nested path
// under no live backend: every declared child's LiveStatus resolves to
// "unreachable" (deadline-bounded, never left empty) — the root itself is
// never probed (only children carry a live verdict).
func TestBuildStatusRootsTree_LiveProbeUnreachable(t *testing.T) {
	const parent = "check-android-emulator-pod"
	opts := CollectOpts{Unified: nestedUnified(), RunMode: "quadlet", Nested: true}
	roots := buildStatusRootsTree(opts, true)

	root := findRootNode(roots, parent)
	if root == nil || len(root.Children) != 2 {
		t.Fatalf("root %q must carry 2 children under nested, got %+v", parent, root)
	}
	if root.LiveStatus != "" {
		t.Errorf("root.LiveStatus = %q, want empty (roots are never live-probed)", root.LiveStatus)
	}
	for _, c := range root.Children {
		if c.LiveStatus != "unreachable" {
			t.Errorf("child %q LiveStatus = %q under nested with no backend, want %q", c.Key, c.LiveStatus, "unreachable")
		}
	}
}

// TestBuildStatusRootsTree_NilConfigNoOp verifies a clean nil/empty result
// when no deploy config is available (opts.Unified == nil, opts.Deploy ==
// nil) — the candy's overlay must never regress on a config-less host.
func TestBuildStatusRootsTree_NilConfigNoOp(t *testing.T) {
	roots := buildStatusRootsTree(CollectOpts{RunMode: "quadlet"}, false)
	if len(roots) != 0 {
		t.Fatalf("nil-config tree must be empty, got %+v", roots)
	}
}
