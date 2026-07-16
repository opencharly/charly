package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/opencharly/sdk/spec"
	"github.com/opencharly/sdk/vmshared"

	"github.com/opencharly/sdk/deploykit"
)

// deploy_node_test.go — tests for BundleNode tree walking and
// dotted-path resolution.

func makeTree() map[string]spec.BundleNode {
	return map[string]spec.BundleNode{
		"stack": {
			Target: "container",
			Children: map[string]*spec.BundleNode{
				"web": {
					Target: "container",
					Children: map[string]*spec.BundleNode{
						"db": {Target: "host"},
					},
				},
				"worker": {Target: "host"},
			},
		},
		"arch": {
			Target: "vm",
			From:   "arch",
		},
	}
}

func TestWalkPreOrder_RootThenChildren(t *testing.T) {
	tree := makeTree()
	root := tree["stack"]
	var paths []string
	err := deploykit.BundleWalkPreOrder(&root, "stack", func(path string, node *spec.BundleNode) error {
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	want := []string{"stack", "stack.web", "stack.web.db", "stack.worker"}
	if !equalSlices(paths, want) {
		t.Errorf("paths = %v, want %v", paths, want)
	}
}

func TestWalkPostOrder_ChildrenThenRoot(t *testing.T) {
	tree := makeTree()
	root := tree["stack"]
	var paths []string
	err := deploykit.BundleWalkPostOrder(&root, "stack", func(path string, node *spec.BundleNode) error {
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	want := []string{"stack.web.db", "stack.web", "stack.worker", "stack"}
	if !equalSlices(paths, want) {
		t.Errorf("paths = %v, want %v", paths, want)
	}
}

func TestResolveNodePath_FindsNested(t *testing.T) {
	tree := makeTree()
	node, ancestors, err := deploykit.ResolveNodePath(tree, "stack.web.db")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if node.Target != "host" {
		t.Errorf("resolved target = %q, want host", node.Target)
	}
	if len(ancestors) != 2 {
		t.Errorf("ancestors len = %d, want 2", len(ancestors))
	}
}

func TestResolveNodePath_MissingSegment(t *testing.T) {
	tree := makeTree()
	_, _, err := deploykit.ResolveNodePath(tree, "stack.missing.db")
	if err == nil {
		t.Fatal("expected error for missing segment")
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Errorf("expected error to name the missing segment, got %v", err)
	}
}

func TestResolveNodePath_EmptyPath(t *testing.T) {
	tree := makeTree()
	_, _, err := deploykit.ResolveNodePath(tree, "")
	if err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestResolveNodePath_MalformedDots(t *testing.T) {
	tree := makeTree()
	for _, bad := range []string{"stack.", ".stack", "stack..web"} {
		if _, _, err := deploykit.ResolveNodePath(tree, bad); err == nil {
			t.Errorf("expected error for malformed path %q", bad)
		}
	}
}

func TestValidateDeploymentTree_RejectsDotInName(t *testing.T) {
	deploy := map[string]spec.BundleNode{
		"bad.name": {Target: "host"},
	}
	err := validateDeploymentTree(deploy)
	if err == nil {
		t.Fatal("expected error for '.' in deployment name")
	}
	if !strings.Contains(err.Error(), "'.'") {
		t.Errorf("error should cite the reserved character, got %v", err)
	}
}

func TestSortedChildKeys_Deterministic(t *testing.T) {
	kids := map[string]*spec.BundleNode{"z": {}, "a": {}, "m": {}}
	got := deploykit.SortedNestedKeys(kids)
	if !equalSlices(got, []string{"a", "m", "z"}) {
		t.Errorf("got %v, want [a m z]", got)
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

// TestMergeDeployConfigsLocalCutoverFields locks in the field-level merge for
// the kind:local target fields: Local, User,
// SSHArgs. Without these, target:local deployments authored in the project
// deploy.yml lost their template ref + ssh overrides whenever resolveTreeRoot
// merged via MergeDeployConfigs(projectDC, localDC), leaving the local deploy
// with an empty candy list and a silent no-op install.
//
// Fixture name `charly-cachyos` matches the deployment key (renamed from `qc`
// in the 2026-05 cross-kind name reuse cutover; the entry itself relocated to
// the opencharly/distro-cachyos submodule in the 2026-05 CachyOS migration).
func TestMergeDeployConfigsLocalCutoverFields(t *testing.T) {
	project := &deploykit.BundleConfig{Bundle: map[string]spec.BundleNode{
		"charly-cachyos": {
			Target:  "local",
			From:    "charly-cachyos",
			Host:    "local",
			User:    "alice",
			SSHArgs: []string{"-o", "ServerAliveInterval=30"},
		},
	}}
	merged := deploykit.MergeDeployConfigs(project, nil)
	got, ok := merged.Bundle["charly-cachyos"]
	if !ok {
		t.Fatal("charly-cachyos dropped by MergeDeployConfigs")
	}
	if got.From != "charly-cachyos" {
		t.Errorf("Local field lost: got %q want %q", got.From, "charly-cachyos")
	}
	if got.User != "alice" {
		t.Errorf("User field lost: got %q", got.User)
	}
	if !equalSlices(got.SSHArgs, []string{"-o", "ServerAliveInterval=30"}) {
		t.Errorf("SSHArgs field lost: got %v", got.SSHArgs)
	}
	// Per-machine overlay wins on collision (mirrors Host's behavior).
	overlay := &deploykit.BundleConfig{Bundle: map[string]spec.BundleNode{
		"charly-cachyos": {From: "ci-runner", User: "bob", SSHArgs: []string{"-o", "ProxyJump=bastion"}},
	}}
	merged = deploykit.MergeDeployConfigs(project, overlay)
	got = merged.Bundle["charly-cachyos"]
	if got.From != "ci-runner" {
		t.Errorf("overlay Local should win: got %q", got.From)
	}
	if got.User != "bob" {
		t.Errorf("overlay User should win: got %q", got.User)
	}
	if !equalSlices(got.SSHArgs, []string{"-o", "ProxyJump=bastion"}) {
		t.Errorf("overlay SSHArgs should win: got %v", got.SSHArgs)
	}
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestMergeDeployConfigsPreservesAllFields locks in the 2026-05 regression
// fix: pre-fix MergeDeployConfigs hand-rolled per-field copies and silently
// dropped 19+ BundleNode fields (ResolvedPort, Description, Secret,
// Sidecar, Shell, Kubernetes, ForwardGpgAgent, ForwardSshAgent, Kind,
// Replica, Restart, Schedule, Resources, Expose, Storage, Probes, Cpus,
// Ram, DiskSize). Any future addition of a struct field would silently
// regress in the same way. The post-fix reflect-based merger walks every
// yaml-tagged field, so adding a new field is automatically merge-correct.
//
// This test pre-populates ALL persistable fields with non-zero values
// and asserts every one survives the merge.
func TestMergeDeployConfigsPreservesAllFields(t *testing.T) {
	tr := true
	rp := []string{"32718:2718"}
	desc := "testing"
	sec := []vmshared.DeploySecretConfig{{Name: "test"}}
	sd := map[string]json.RawMessage{"side": json.RawMessage(`{"image":"img"}`)}
	shl := []DeployShellOverlay{{ID: "x"}}
	k8s := &K8sDeployConfig{Namespace: "test-ns"}
	res := &vmshared.DeployResources{}
	exp := &vmshared.DeployExpose{Host: "example.com", TLS: true}
	storage := []vmshared.DeployStorage{{Name: "s"}}
	probes := &vmshared.DeployProbes{}

	src := spec.BundleNode{
		ResolvedPort:    rp,
		Description:     desc,
		Secret:          sec,
		ForwardGpgAgent: &tr,
		ForwardSshAgent: &tr,
		Sidecar:         sd,
		Shell:           shl,
		Kubernetes:      k8s,
		Kind:            "service",
		Replica:         3,
		Restart:         "always",
		Schedule:        "* * * * *",
		Resources:       res,
		Expose:          exp,
		Storage:         storage,
		Probes:          probes,
		Cpus:            4,
		Ram:             "16G",
		DiskSize:        "40G",
	}
	cfg := &deploykit.BundleConfig{Bundle: map[string]spec.BundleNode{"x": src}}
	merged := deploykit.MergeDeployConfigs(cfg, nil)
	got := merged.Bundle["x"]

	checks := []struct {
		name string
		fail bool
	}{
		{"ResolvedPort", !equalSlices(got.ResolvedPort, rp)},
		{"Description", got.Description == ""},
		{"Secret", len(got.Secret) != 1},
		{"ForwardGpgAgent", got.ForwardGpgAgent == nil || !*got.ForwardGpgAgent},
		{"ForwardSshAgent", got.ForwardSshAgent == nil || !*got.ForwardSshAgent},
		{"Sidecar", len(got.Sidecar) != 1},
		{"Shell", len(got.Shell) != 1},
		{"Kubernetes", got.Kubernetes == nil},
		{"Kind", got.Kind != "service"},
		{"Replica", got.Replica != 3},
		{"Restart", got.Restart != "always"},
		{"Schedule", got.Schedule != "* * * * *"},
		{"Resources", got.Resources == nil},
		{"Expose", got.Expose == nil},
		{"Storage", len(got.Storage) != 1},
		{"Probes", got.Probes == nil},
		{"Cpus", got.Cpus != 4},
		{"Ram", got.Ram != "16G"},
		{"DiskSize", got.DiskSize != "40G"},
	}
	dropped := []string{}
	for _, c := range checks {
		if c.fail {
			dropped = append(dropped, c.name)
		}
	}
	if len(dropped) > 0 {
		t.Errorf("MergeDeployConfigs dropped %d fields: %v", len(dropped), dropped)
	}
}
