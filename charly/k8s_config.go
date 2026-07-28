package main

import (
	"github.com/opencharly/sdk/loaderkit"
)

// -----------------------------------------------------------------------------
// vmshared.K8sDeployConfig — the `kubernetes:` sub-block on BundleNode. Part F.
//
// Schema v4: deploy-side K8s knobs (namespace, workload kind override,
// patches, raw manifests) stay here. Cluster-wide policy (kubeconfig
// context, admission policy, storage, ingress defaults, etc.) lives on the
// K8sSpec (kind:k8s template, generated in spec/cue_types_gen.go), referenced
// via BundleNode.K8s — the legacy per-deploy `cluster` string field was removed
// in that v4 cutover.
// -----------------------------------------------------------------------------

// Schema v4: ClusterProfile / LoadClusterProfile / clusters/*.yaml loaders
// have been removed. Cluster config lives on K8sSpec (kind:k8s entities in
// charly.yml / k8s.yml). `charly migrate` synthesizes a kind:k8s
// entry from any pre-existing clusters/<name>.yaml.

// findK8sSpec looks up a K8sSpec by name from the project's charly.yml / k8s.yml
// via the unified loader. Returns nil if no matching kind:k8s entity exists or if
// the unified file can't be loaded. This is the CLIENT-GO-FREE cluster-context
// resolver: the host serves it behind the GENERIC "deploy-entity-resolve" HostBuild
// seam (kind:k8s → ResolvedK8s.KubeconfigContext), which the out-of-process
// candy/plugin-kube provider Invokes to resolve a `cluster: <name>` profile to a
// concrete kubeconfig context (both the kube: verb and the k3s/preresolve legs) —
// the plugin cannot reach the project loader itself. Also consumed by
// k8s_deploy_from_box.go (source-less `charly bundle from-box --target k8s`).
//
// K1-unblock wave 2: name is resolved through loaderkit.ProjectTemplates' namespace-qualified template map
// (the SAME projection resolved_project_host.go's "resolved-project" envelope ships, minus the
// full box-resolution cost that envelope also pays — loaderkit.ProjectTemplates is a cheap raw-byte copy, no
// ResolveBox calls) instead of a bare uf.K8s[name] lookup. This is a genuine functional fix, not
// just a relocation: the bare lookup never supported a namespace-qualified `--cluster ns.name`
// profile at all; the namespace-flattened map does.
func findK8sSpec(dir, name string) *ResolvedK8s {
	if dir == "" || name == "" {
		return nil
	}
	uf, _, err := LoadUnified(dir)
	if err != nil || uf == nil {
		return nil
	}
	t := loaderkit.ProjectTemplates(uf)
	if t == nil || t.K8s == nil {
		return nil
	}
	body, ok := t.K8s[name]
	if !ok {
		return nil
	}
	r, rerr := resolveK8sViaPlugin(body)
	if rerr != nil {
		return nil
	}
	return r
}
