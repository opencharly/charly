package main

import "os"

// -----------------------------------------------------------------------------
// K8sDeployConfig — the `kubernetes:` sub-block on BundleNode. Part F.
//
// Schema v4: deploy-side K8s knobs (namespace, workload kind override,
// patches, raw manifests) stay here. Cluster-wide policy (kubeconfig
// context, admission policy, storage, ingress defaults, etc.) lives on the
// K8sSpec (kind:k8s template, generated in spec/cue_types_gen.go), referenced
// via BundleNode.K8s — the legacy per-deploy `cluster` string field was removed
// in that v4 cutover.
// -----------------------------------------------------------------------------

// K8sPatchTarget identifies which generated resource a patch applies to.
type K8sPatchTarget struct {
	Kind      string `yaml:"kind,omitempty" json:"kind,omitempty"`
	Name      string `yaml:"name,omitempty" json:"name,omitempty"`
	Namespace string `yaml:"namespace,omitempty" json:"namespace,omitempty"`
}

// Schema v4: ClusterProfile / LoadClusterProfile / clusters/*.yaml loaders
// have been removed. Cluster config lives on K8sSpec (kind:k8s entities in
// charly.yml / k8s.yml). `charly migrate` synthesizes a kind:k8s
// entry from any pre-existing clusters/<name>.yaml.

// findK8sSpec looks up a K8sSpec by name from the project's charly.yml / k8s.yml
// via the unified loader. Returns nil if no matching kind:k8s entity exists or if
// the unified file can't be loaded. This is the CLIENT-GO-FREE cluster-context
// resolver: the host uses it to resolve a `--cluster <name>` profile to a
// concrete kubeconfig context (resolveClusterContext, the CheckContext.ResolveClusterContext
// reverse-leg the out-of-process candy/plugin-kube provider pulls) — the plugin cannot reach the
// project loader itself. Also consumed by k8s_deploy_from_box.go (source-less
// `charly bundle from-box --target k8s`).
func findK8sSpec(dir, name string) *ResolvedK8s {
	if dir == "" || name == "" {
		return nil
	}
	uf, _, err := LoadUnified(dir)
	if err != nil || uf == nil || uf.K8s == nil {
		return nil
	}
	body, ok := uf.K8s[name]
	if !ok {
		return nil
	}
	r, rerr := resolveK8sViaPlugin(body)
	if rerr != nil {
		return nil
	}
	return r
}

// resolveClusterContext maps a k8s cluster-profile NAME to its kubeconfig context via the
// project loader (findK8sSpec). It is the host-side leg for CheckContext.ResolveClusterContext
// — the out-of-process candy/plugin-kube provider builds its rest.Config from kubeconfig +
// context but cannot reach the project loader, so the plugin PULLS the context through this
// reverse-leg (replacing the former host op-rewrite). An empty context
// (no matching kind:k8s profile) is a valid result — the plugin falls back to the kubeconfig
// current-context (the same behavior the in-tree restConfig had).
func (r *Runner) resolveClusterContext(cluster string) (string, error) {
	if cluster == "" {
		return "", nil
	}
	cwd, _ := os.Getwd()
	spec := findK8sSpec(cwd, cluster)
	if spec == nil {
		return "", nil
	}
	return spec.KubeconfigContext, nil
}
