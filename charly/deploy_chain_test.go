package main

import (
	"strings"
	"testing"

	"github.com/opencharly/sdk/kit"
)

// stampTestDescents mirrors the substrate loader: it stamps the descent-descriptor
// candy/plugin-substrate adds at OpLoad (recursing the nested/peer subtree), so chain
// unit tests — which build BundleNode literals directly, bypassing the loader — run
// against realistically-stamped nodes instead of tripping the nil-descent guard.
func stampTestDescents(roots map[string]BundleNode) map[string]BundleNode {
	out := make(map[string]BundleNode, len(roots))
	for k, v := range roots {
		n := v
		kit.StampDescent(&n)
		out[k] = n
	}
	return out
}

// TestResolveDeployChain_FlatContainer verifies a single-segment pod path
// produces a one-hop NestedExecutor with JumpPodmanExec into "charly-<name>".
func TestResolveDeployChain_FlatContainer(t *testing.T) {
	roots := map[string]BundleNode{
		"redis": {Target: "pod"},
	}
	leaf, chain, err := ResolveDeployChain(stampTestDescents(roots), "redis", ShellExecutor{})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if leaf == nil || leaf.Target != "pod" {
		t.Fatalf("unexpected leaf: %+v", leaf)
	}
	venue := chain.Venue()
	// Expect: nested:podman-exec:charly-redis/local
	if !strings.Contains(venue, "podman-exec:charly-redis") {
		t.Errorf("venue %q does not contain podman-exec:charly-redis", venue)
	}
}

// TestResolveDeployChain_VmFlat verifies a single-segment vm path returns
// a plain SSHExecutor (no NestedExecutor wrapper at the root level).
func TestResolveDeployChain_VmFlat(t *testing.T) {
	roots := map[string]BundleNode{
		"bench-vm": {
			Target: "vm",
			VmState: &VmDeployState{
				SshUser: "arch",
				SshPort: 2222,
			},
		},
	}
	_, chain, err := ResolveDeployChain(stampTestDescents(roots), "bench-vm", ShellExecutor{})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	venue := chain.Venue()
	// Post-cutover: SSH connection details live in the managed ssh-config
	// fragment (~/.config/charly/ssh_config) under the alias charly-<deployName>;
	// SSHExecutor needs only the alias as Host. Expect: ssh://charly-bench-vm
	if !strings.Contains(venue, "ssh://charly-bench-vm") {
		t.Errorf("venue %q does not contain ssh://charly-bench-vm (managed alias)", venue)
	}
}

// TestResolveDeployChain_VmInnerPod is the critical multi-hop case: a
// pod nested inside a VM. Must produce a chain where the leaf hop is
// JumpPodmanExec into the flattened name "charly-bench-vm_inner".
func TestResolveDeployChain_VmInnerPod(t *testing.T) {
	innerNode := &BundleNode{Target: "pod"}
	roots := map[string]BundleNode{
		"bench-vm": {
			Target: "vm",
			VmState: &VmDeployState{
				SshUser: "arch",
				SshPort: 2222,
			},
			Children: map[string]*BundleNode{
				"inner": innerNode,
			},
		},
	}
	leaf, chain, err := ResolveDeployChain(stampTestDescents(roots), "bench-vm.inner", ShellExecutor{})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if leaf != innerNode {
		t.Errorf("leaf mismatch: got %+v, want %+v", leaf, innerNode)
	}
	venue := chain.Venue()
	// A pod nested in a VM guest is deployed STANDALONE by the guest's own
	// `charly bundle from-box <ref> <childKey>` (plugin-deploy-vm's PostApply), so the
	// in-guest container is "charly-<childKey>" (the leaf) — NOT the host-side
	// "charly-<vm>_<inner>" flatPath the guest never sees. The chain must podman-exec
	// the leaf name, or it targets a container that doesn't exist (the silent
	// single-hop the nested-pod-in-VM check hit before this fix).
	if !strings.Contains(venue, "podman-exec:charly-inner") {
		t.Errorf("venue %q does not contain podman-exec:charly-inner (in-guest leaf name)", venue)
	}
	// And the chain should also contain the SSH hop earlier.
	if !strings.Contains(venue, "ssh:") {
		t.Errorf("venue %q does not contain ssh hop", venue)
	}
}

// TestResolveDeployChain_ThreeDeep stacks three hops:
// vm → inner-pod → nested-pod. Verifies arbitrary depth works.
func TestResolveDeployChain_ThreeDeep(t *testing.T) {
	deepNode := &BundleNode{Target: "pod"}
	innerNode := &BundleNode{
		Target: "pod",
		Children: map[string]*BundleNode{
			"deeper": deepNode,
		},
	}
	roots := map[string]BundleNode{
		"bench-vm": {
			Target: "vm",
			VmState: &VmDeployState{
				SshUser: "arch",
				SshPort: 2222,
			},
			Children: map[string]*BundleNode{
				"inner": innerNode,
			},
		},
	}
	leaf, chain, err := ResolveDeployChain(stampTestDescents(roots), "bench-vm.inner.deeper", ShellExecutor{})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if leaf != deepNode {
		t.Errorf("leaf mismatch")
	}
	venue := chain.Venue()
	// Expect chain: ...podman-exec:charly-bench-vm_inner_deeper / podman-exec:charly-bench-vm_inner / ssh: ...
	if !strings.Contains(venue, "podman-exec:charly-bench-vm_inner_deeper") {
		t.Errorf("venue %q does not contain leaf hop podman-exec:charly-bench-vm_inner_deeper", venue)
	}
	if !strings.Contains(venue, "podman-exec:charly-bench-vm_inner") {
		t.Errorf("venue %q does not contain intermediate hop podman-exec:charly-bench-vm_inner", venue)
	}
}

// TestResolveDeployChain_UnknownRoot returns a clear error with the
// "available deployments" hint.
func TestResolveDeployChain_UnknownRoot(t *testing.T) {
	roots := map[string]BundleNode{
		"redis": {Target: "pod"},
		"web":   {Target: "pod"},
	}
	_, _, err := ResolveDeployChain(stampTestDescents(roots), "missing", ShellExecutor{})
	if err == nil {
		t.Fatal("expected error for unknown root")
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Errorf("error %q does not mention missing name", err)
	}
	if !strings.Contains(err.Error(), "available deployments") {
		t.Errorf("error %q does not include did-you-mean hint", err)
	}
}

// TestResolveDeployChain_UnknownNestedChild returns a hint about
// available nested children.
func TestResolveDeployChain_UnknownNestedChild(t *testing.T) {
	roots := map[string]BundleNode{
		"vm": {
			Target: "vm",
			VmState: &VmDeployState{
				SshUser: "arch",
				SshPort: 2222,
			},
			Children: map[string]*BundleNode{
				"inner-app": {Target: "pod"},
			},
		},
	}
	_, _, err := ResolveDeployChain(stampTestDescents(roots), "vm.missing-child", ShellExecutor{})
	if err == nil {
		t.Fatal("expected error for unknown nested child")
	}
	if !strings.Contains(err.Error(), "missing-child") {
		t.Errorf("error %q does not mention missing-child", err)
	}
	if !strings.Contains(err.Error(), "available nested children") {
		t.Errorf("error %q does not include nested-children hint", err)
	}
}

// TestImageChain produces a JumpPodmanRun chain.
func TestImageChain(t *testing.T) {
	chain := ImageChain("podman", "fedora-coder:latest")
	venue := chain.Venue()
	if !strings.Contains(venue, "podman-run:fedora-coder:latest") {
		t.Errorf("venue %q does not contain podman-run hop", venue)
	}
}

// TestContainerChain produces a JumpPodmanExec chain into the literal name.
func TestContainerChain(t *testing.T) {
	chain := ContainerChain("podman", "charly-redis")
	venue := chain.Venue()
	if !strings.Contains(venue, "podman-exec:charly-redis") {
		t.Errorf("venue %q does not contain podman-exec:charly-redis", venue)
	}
}
