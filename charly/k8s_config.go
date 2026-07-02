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
// resolver: the host uses it to pre-resolve a `--cluster <name>` profile to a
// concrete kubeconfig context (preresolveKubeCluster) BEFORE marshaling a `kube:`
// Op to the out-of-process candy/plugin-kube provider, which cannot reach the
// project loader itself. Also consumed by k8s_deploy_from_box.go (source-less
// `charly bundle from-box --target k8s`).
func findK8sSpec(dir, name string) *K8sSpec {
	if dir == "" || name == "" {
		return nil
	}
	uf, _, err := LoadUnified(dir)
	if err != nil || uf == nil {
		return nil
	}
	if uf.K8s == nil {
		return nil
	}
	return uf.K8s[name]
}

// preresolveKubeCluster turns a `kube:` op's `cluster: <profile>` into a concrete
// kubeconfig context for the out-of-process candy/plugin-kube provider. The plugin
// builds its rest.Config from kubeconfig + context, but it cannot reach core's
// project loader (findK8sSpec) to map a ClusterProfile NAME to a kubeconfig context
// — so the host resolves it HERE, copy-on-write, before invokeVerbProvider marshals
// the Op. Returns c unchanged for a non-kube op, an op with no `cluster:`, or an op
// that already carries an explicit `kube_context:`. When no kind:k8s profile matches
// the cluster name, the context stays empty and the plugin falls back to the
// kubeconfig current-context (the same behavior the in-tree restConfig had).
func preresolveKubeCluster(c *Op) *Op {
	if c.Kube == "" || c.Cluster == "" || c.KubeContext != "" {
		return c
	}
	cwd, _ := os.Getwd()
	spec := findK8sSpec(cwd, c.Cluster)
	if spec == nil || spec.KubeconfigContext == "" {
		return c
	}
	cc := *c
	cc.KubeContext = spec.KubeconfigContext
	return &cc
}
