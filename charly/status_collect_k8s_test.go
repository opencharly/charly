package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// k8sUnified builds a minimal UnifiedFile projection with one target:k8s
// deploy named `name` referencing a kind:k8s template `tmpl` with context
// `ctx`, plus an unrelated pod deploy that the collector must ignore.
func k8sUnified(name, image, tmpl, ctx string) *UnifiedFile {
	return &UnifiedFile{
		Bundle: map[string]BundleNode{
			name:       {Target: "k8s", Image: image, From: tmpl},
			"some-pod": {Target: "pod", Image: "redis"},
		},
		K8s: map[string]*K8sSpec{
			tmpl: {Box: image, KubeconfigContext: ctx, DefaultNamespace: "apps"},
		},
	}
}

// TestK8sCollector_TreePresent builds a real .opencharly/k8s/<name>/ tree under
// a temp cwd and asserts the collector emits one tree-present row stamped
// Kind=k8s, Source=tree, with the template's kubeconfig context surfaced.
func TestK8sCollector_TreePresent(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	const name, image, tmpl, ctx = "openclaw", "openclaw", "prod-cluster", "gke_prod"

	// Craft the generated Kustomize tree at .opencharly/k8s/<name>/base/.
	baseDir := filepath.Join(dir, ".opencharly", "k8s", name, "base")
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		t.Fatalf("mkdir tree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(baseDir, "deployment.yaml"), []byte("kind: Deployment\n"), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	col := &K8sCollector{}
	opts := CollectOpts{Unified: k8sUnified(name, image, tmpl, ctx), RunMode: "quadlet"}

	if !col.Available(opts) {
		t.Fatalf("Available = false, want true (tree dir + declared deploy)")
	}

	rows, err := col.Collect(context.Background(), opts)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1 (pod deploy must be ignored): %+v", len(rows), rows)
	}
	r := rows[0]
	if r.Kind != SubstrateK8s {
		t.Errorf("Kind = %q, want %q", r.Kind, SubstrateK8s)
	}
	if r.Source != "tree" {
		t.Errorf("Source = %q, want %q", r.Source, "tree")
	}
	if r.Status != "tree-present" {
		t.Errorf("Status = %q, want %q", r.Status, "tree-present")
	}
	if r.Container != name {
		t.Errorf("Container = %q, want %q", r.Container, name)
	}
	if r.Image != image {
		t.Errorf("Image = %q, want %q", r.Image, image)
	}
	if r.Network != ctx {
		t.Errorf("Network (context) = %q, want %q", r.Network, ctx)
	}
	if r.RunMode != "quadlet" {
		t.Errorf("RunMode = %q, want %q", r.RunMode, "quadlet")
	}
}

// TestK8sCollector_NotGenerated asserts a declared target:k8s deploy with no
// on-disk tree reports not-generated (and still surfaces the context).
func TestK8sCollector_NotGenerated(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	const name, image, tmpl, ctx = "billing", "billing", "stage", "eks_stage"

	col := &K8sCollector{}
	opts := CollectOpts{Unified: k8sUnified(name, image, tmpl, ctx)}

	// No tree on disk, but a target:k8s deploy is declared → Available true.
	if !col.Available(opts) {
		t.Fatalf("Available = false, want true (declared deploy)")
	}

	rows, err := col.Collect(context.Background(), opts)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1: %+v", len(rows), rows)
	}
	r := rows[0]
	if r.Status != "not-generated" {
		t.Errorf("Status = %q, want %q", r.Status, "not-generated")
	}
	if r.Kind != SubstrateK8s {
		t.Errorf("Kind = %q, want %q", r.Kind, SubstrateK8s)
	}
	if r.Network != ctx {
		t.Errorf("Network (context) = %q, want %q", r.Network, ctx)
	}
}

// TestK8sCollector_AvailableFalse asserts the collector is skipped silently
// when there is neither a tree dir nor any declared target:k8s deploy.
func TestK8sCollector_AvailableFalse(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	col := &K8sCollector{}

	// Nil unified, no tree.
	if col.Available(CollectOpts{}) {
		t.Errorf("Available = true with nil unified + no tree, want false")
	}

	// Only a pod deploy declared — still nothing for the k8s substrate.
	uf := &UnifiedFile{Bundle: map[string]BundleNode{"web": {Target: "pod", Image: "web"}}}
	if col.Available(CollectOpts{Unified: uf}) {
		t.Errorf("Available = true with only a pod deploy, want false")
	}

	rows, err := col.Collect(context.Background(), CollectOpts{Unified: uf})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("rows = %d, want 0 for a pod-only projection", len(rows))
	}
}

// TestK8sWorkloadKind was removed with the --nested live-readiness probe (the
// workload-kind heuristic + GVR mapping left charly's core for candy/plugin-kube in
// the kube → external-plugin dep-shed). The remaining collector tests assert
// tree-state only.
