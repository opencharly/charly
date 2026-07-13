package main

import (
	"context"
	"os"
	"path/filepath"

	"github.com/opencharly/sdk/spec"
)

// K8sCollector is the kubernetes SubstrateCollector. It surfaces every
// declared `target: k8s` deployment (from the folded charly.yml Deploy
// map) as one DeploymentStatus row, reporting whether its Kustomize tree has
// been generated under .opencharly/k8s/<name>/ and which cluster/context the
// referenced kind:k8s template points at.
//
// Provenance is Source="tree": k8s deploys do not run a container on this
// host — they emit a manifest tree that `charly bundle sync` / `kubectl apply -k`
// applies to a remote cluster. The collector reports generation state
// (tree-present | not-generated), never live pod health: live cluster readiness
// would require the client-go subset, which left charly's core go.mod for
// the out-of-tree candy/plugin-kube (no host loads the plugin at `charly status`
// time), so the former --nested live-readiness probe was dropped. A k8s
// deployment's live health is asserted by a `kube:` check (candy/plugin-kube),
// not the status collector.
type K8sCollector struct {
	c *Collector
}

func init() {
	registerSubstrate(func(c *Collector) SubstrateCollector { return &K8sCollector{c: c} })
}

// Kind reports the k8s substrate.
func (k *K8sCollector) Kind() spec.SubstrateKind { return spec.SubstrateK8s }

// Available reports true when this host has any k8s footprint: either the
// .opencharly/k8s tree directory exists, or at least one target:k8s deploy is
// declared in the projected charly.yml. An absent unified projection plus
// an absent tree dir means there is nothing to report — skipped silently.
func (k *K8sCollector) Available(opts CollectOpts) bool {
	if dir, err := k8sTreeRoot(); err == nil {
		if _, statErr := os.Stat(dir); statErr == nil {
			return true
		}
	}
	return len(k8sDeployEntries(opts.Unified)) > 0
}

// Collect emits one row per declared target:k8s deploy. It stats
// .opencharly/k8s/<name>/ (tree-present | not-generated) and reads
// cluster/context from the referenced kind:k8s template. There is no live cluster
// readiness probe — that left for candy/plugin-kube with the client-go subset (see
// the type docstring); a `kube:` check asserts live health instead.
func (k *K8sCollector) Collect(ctx context.Context, opts CollectOpts) ([]spec.DeploymentStatus, error) {
	entries := k8sDeployEntries(opts.Unified)
	if len(entries) == 0 {
		return nil, nil
	}
	treeRoot, rootErr := k8sTreeRoot()

	rows := make([]spec.DeploymentStatus, 0, len(entries))
	for _, name := range entries {
		node := opts.Unified.Bundle[name]

		row := spec.DeploymentStatus{
			Kind:      spec.SubstrateK8s,
			Source:    "tree",
			Image:     k8sImageRef(name, node),
			Container: name,
			RunMode:   opts.RunMode,
		}

		// Tree-present detection: <treeRoot>/<name>/ stats successfully iff
		// `charly bundle add` has generated the Kustomize tree.
		treePresent := false
		if rootErr == nil {
			if _, err := os.Stat(filepath.Join(treeRoot, name)); err == nil {
				treePresent = true
			}
		}
		if treePresent {
			row.Status = "tree-present"
		} else {
			row.Status = "not-generated"
		}

		// Cluster/context from the referenced kind:k8s template. Network is
		// the contextual cell used to show where the workload points.
		ks := k8sSpecFor(opts.Unified, node)
		if ks != nil && ks.KubeconfigContext != "" {
			row.Network = ks.KubeconfigContext
		} else if node.From != "" {
			row.Network = node.From
		}

		rows = append(rows, row)
	}
	return rows, nil
}

// k8sTreeRoot returns <cwd>/.opencharly/k8s — the canonical root that
// defaultK8sOutputDir (deploy_add_cmd_k8s.go) emits Kustomize trees under.
func k8sTreeRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Join(cwd, ".opencharly", "k8s"), nil
}

// k8sDeployEntries returns the names of every target:k8s deploy in the folded
// Bundle map, in deterministic (map-key) order via the shared classifier so
// legacy "kubernetes" spellings resolve identically to "k8s".
func k8sDeployEntries(uf *UnifiedFile) []string {
	if uf == nil || uf.Bundle == nil {
		return nil
	}
	var names []string
	for name, node := range uf.Bundle {
		n := node
		if classifyNodeTarget(&n, name) == "k8s" {
			names = append(names, name)
		}
	}
	return names
}

// k8sImageRef resolves the image a k8s deploy runs, mirroring the k8s deploy
// preresolver (k8s_deploy_preresolve.go): the node's explicit Box, falling back
// to the deploy name.
func k8sImageRef(name string, node BundleNode) string {
	if node.Image != "" {
		return node.Image
	}
	return name
}

// k8sSpecFor resolves the kind:k8s template referenced by node.From from the
// unified projection. Nil when unreferenced or absent.
func k8sSpecFor(uf *UnifiedFile, node BundleNode) *ResolvedK8s {
	if uf == nil || uf.K8s == nil || node.From == "" {
		return nil
	}
	body, ok := uf.K8s[node.From]
	if !ok {
		return nil
	}
	r, err := resolveK8sViaPlugin(body)
	if err != nil {
		return nil
	}
	return r
}

// The former --nested live-readiness probe (the client-go dynamic-client workload
// query + its workload-kind/namespace helpers) was removed in the kube →
// external-plugin dep-shed: it depended on the client-go dynamic client +
// apimachinery, which left charly's core go.mod for candy/plugin-kube. `charly
// status` reports the k8s deploy's tree-state only; a `kube: wait-ready` /
// `kube: nodes` check (candy/plugin-kube) asserts live workload health.
