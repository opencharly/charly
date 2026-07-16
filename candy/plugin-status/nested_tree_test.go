package status

import (
	"testing"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// nested_tree_test.go — ported from charly/status_nested_test.go (K5): the declared
// nested-deployment tree pre-resolution moved plugin-side (nested_tree.go), so these tests moved
// with it. Exercises the PURE buildStatusRootsTreeFrom/mergedNestedRootsFrom split directly with
// in-memory deploykit.BundleNode fixtures — no executor stub, no host file I/O (R3, mirrors
// candy/plugin-substrate's collectAndroidDeployNodes test pattern). The PURE fold
// (claim -> inherit real data -> drop from top level; synthesize declared/nested; sorted order;
// dedup) lives in overlay.go, operating on the []spec.StatusNestedNode this file builds — its
// byte-parity is proven by overlay_golden_test.go. This file proves the pre-resolution alone: the
// tree shape, the MatchKeys candidate order, Kind classification, and the --nested live-probe
// threading.

// nestedRoots builds a minimal declared roots map carrying one declared nested topology: a
// target:pod parent check-android-emulator-pod with two target:android nested children device and
// device-net (the check-android-emulator-pod shape), plus an unrelated flat pod deploy the
// tree-builder must leave alone (no root emitted for a childless entry).
func nestedRoots() map[string]deploykit.BundleNode {
	return map[string]deploykit.BundleNode{
		"check-android-emulator-pod": {
			Target: "pod",
			Image:  "android-emulator",
			Children: map[string]*deploykit.BundleNode{
				"device":     {Target: "android", From: "pixel9a-36", AddCandy: []string{"android-test-apps"}},
				"device-net": {Target: "android", From: "pixel9a-endpoint", AddCandy: []string{"android-apidemos"}},
			},
		},
		"some-flat-pod": {Target: "pod", Image: "redis"},
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

// TestBuildStatusRootsTreeFrom_ChildlessRootSkipped verifies a declared entry with no children
// (some-flat-pod) never emits a root node — the candy overlay skips a childless root anyway
// (`if !root.HasChildren() { continue }`).
func TestBuildStatusRootsTreeFrom_ChildlessRootSkipped(t *testing.T) {
	roots := buildStatusRootsTreeFrom(nestedRoots(), false)
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

// TestBuildStatusRootsTreeFrom_DeclaredChildren verifies the root + child shape: root
// MatchKeys=[key], HasChildren=true; each android child's Kind, sorted order, and
// MatchKeys=[dottedPath, flattenedName] (in that priority order).
func TestBuildStatusRootsTreeFrom_DeclaredChildren(t *testing.T) {
	const parent = "check-android-emulator-pod"
	roots := buildStatusRootsTreeFrom(nestedRoots(), false)

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

// TestBuildStatusRootsTreeFrom_MatchKeysOrderVmPod verifies the MatchKeys candidate order for a
// nested-pod-in-vm child: dotted path first, the flattened NestedContainerName second — the SAME
// priority order the former claimFlatRow tried them in.
func TestBuildStatusRootsTreeFrom_MatchKeysOrderVmPod(t *testing.T) {
	const parent = "stack-vm"
	roots := map[string]deploykit.BundleNode{
		parent: {
			Target: "vm",
			From:   "stack-vm",
			Children: map[string]*deploykit.BundleNode{
				"web": {Target: "pod", Image: "nginx"},
			},
		},
	}
	tree := buildStatusRootsTreeFrom(roots, false)

	root := findRootNode(tree, parent)
	if root == nil {
		t.Fatalf("root %q not found in %+v", parent, tree)
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

// TestBuildStatusRootsTreeFrom_LiveProbeUnreachable verifies the --nested path under no live
// backend: every declared child's LiveStatus resolves to "unreachable" (deadline-bounded, never
// left empty) — the root itself is never probed (only children carry a live verdict).
func TestBuildStatusRootsTreeFrom_LiveProbeUnreachable(t *testing.T) {
	const parent = "check-android-emulator-pod"
	roots := buildStatusRootsTreeFrom(nestedRoots(), true)

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

// TestBuildStatusRootsTreeFrom_NilConfigNoOp verifies a clean nil/empty result when no deploy
// config is available — the candy overlay must never regress on a config-less host.
func TestBuildStatusRootsTreeFrom_NilConfigNoOp(t *testing.T) {
	roots := buildStatusRootsTreeFrom(nil, false)
	if len(roots) != 0 {
		t.Fatalf("nil-config tree must be empty, got %+v", roots)
	}
}

// TestMergedNestedRootsFrom_PerMachineWinsPerKey verifies the merge precedence: a per-machine
// overlay entry wins over the project entry sharing the same key (mirrors
// candy/plugin-substrate's TestCollectAndroidDeployNodes_PerMachineWinsPerKey for the SAME
// deploykit.MergeDeployConfigs primitive).
func TestMergedNestedRootsFrom_PerMachineWinsPerKey(t *testing.T) {
	rp := &spec.ResolvedProject{
		Deploy: map[string]*spec.Deploy{
			"redis": {Target: "pod", Image: "redis"},
		},
	}
	perMachine := &deploykit.BundleConfig{
		Bundle: map[string]deploykit.BundleNode{
			"redis": {Target: "vm", From: "redis-vm"},
		},
	}
	merged := mergedNestedRootsFrom(rp, perMachine)
	got, ok := merged["redis"]
	if !ok {
		t.Fatalf("merged roots missing %q: %+v", "redis", merged)
	}
	if got.Target != "vm" || string(got.From) != "redis-vm" {
		t.Errorf("merged[%q] = %+v, want the per-machine override (target=vm from=redis-vm)", "redis", got)
	}
}
