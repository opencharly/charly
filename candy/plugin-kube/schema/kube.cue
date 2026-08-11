// This out-of-tree plugin's OWN CUE schema, served over the Describe channel — the
// typed plugin_input for the `kube` cluster-probe check verb. It is the SINGLE
// SOURCE for this plugin's params, used two ways (the same contract core `spec` and
// the http plugin use):
//
//  1. GENERATE the Go param struct — `cue exp gengotypes` (driven by task cue:gen,
//     which wraps this with `package params` + `@go(params)`) emits
//     ../params/cue_types_gen.go, so the provider decodes plugin_input into a TYPED
//     struct, never a hand-parsed map.
//  2. VALIDATE authored input AT RUNTIME — the plugin serves this source over the
//     Describe channel; the host splices it onto the base (base ++ plugin) and
//     validates every authored `kube:` step's plugin_input against #KubeInput.
//
// Since the schema-compaction cutover the per-verb fields left core #Op: a step's
// `kube: <method>` sugar desugars to the internal plugin/plugin_input pair, the
// method name rides the input's `method` key (the former core #KubeMethod enum),
// and every kube-exclusive modifier (name/namespace/label/cluster/manifest/
// kube_kind/kube_context/kubeconfig/kube_count/kube_resource/kube_group/
// kube_version/json/artifact_key/deploy_name) lives HERE. Only the genuinely SHARED
// step modifiers (timeout, the exit_status/stdout/stderr matchers, context, …) stay
// on core #Op, read off the step Op by the provider. This plugin's own provider.go
// self-resolves a `cluster:` profile to a concrete `kube_context` — into the input
// map — and plugin.go synthesizes the internal {method: k3s-post-provision,
// artifact_key, deploy_name} input (S3, FINAL/K5 unit 6 — the k3s post-provision
// finalization — kubeconfig retrieval, guest-forward rewrite, and the kubeconfig
// merge — moved wholesale into this plugin from charly/k3s_post.go; the VM-forward
// resolution self-loads the project PLUGIN-SIDE now too, K-wave W3a A3-phase-2 — no
// HostBuild round trip left in either leg).
//
// SELF-CONTAINED: it references NO base def, so it compiles standalone (the SDK's
// serve-side check + gengotypes) AND splices onto the base (base ++ plugin is a
// def-name collision check, not a base-reference resolver).
//
// The plugin ALSO serves deploy:kubernetes (the `target: kubernetes` substrate) — that capability
// keeps its authoring contract on core #Deploy / #Kubernetes and carries NO plugin_input,
// so no input def for it lives here.

// #KubeInput is the `kube` verb's plugin_input: the method name plus its
// method-exclusive modifiers.
#KubeInput: {
	// method — the kube method name (the former core #KubeMethod enum plus the
	// internal k3s-post-provision the host synthesizes; the verb's PRIMARY input
	// field, so `kube: nodes` desugars to {method: "nodes"}).
	method: ("nodes" | "wait-nodes" | "pods" | "wait-ready" | "ingress" | "ingressclass" | "storageclass" | "service" | "lb-external-ip" | "addons" | "apply" | "delete" | "raw" | "k3s-post-provision") @go(Method,type=string)
	// name / namespace / label — resource identity + selector.
	name?:      string
	namespace?: string
	label?:     string
	// cluster — a kind:kubernetes cluster template name; this PLUGIN self-resolves it to a
	// concrete kube_context (self-loading the project, K-wave W3a A3-phase-2) and
	// leaves the authored key in place, so the input def admits both.
	cluster?: string
	// manifest — the multi-doc YAML path (apply/delete).
	manifest?: string
	// kube_kind / kube_count — wait-ready's workload kind + wait-nodes' Ready count.
	kube_kind?:  string @go(KubeKind)
	kube_count?: int    @go(KubeCount,type=int)
	// kubeconfig / kube_context — the cluster-selection pair (kubeconfig path +
	// context) an authored step may set explicitly.
	kubeconfig?:   string
	kube_context?: string @go(KubeContext)
	// kube_resource / kube_group / kube_version / json — the raw escape hatch's
	// GVR + JSON output toggle.
	kube_resource?: string @go(KubeResource)
	kube_group?:    string @go(KubeGroup)
	kube_version?:  string @go(KubeVersion)
	json?:          bool   @go(JSON)
	// artifact_key / deploy_name / vm_entity — the k3s-post-provision payload (S3, FINAL/K5 unit
	// 6; re-split by task #18): artifact_key is now the per-deploy DOMAIN-scoped identity (the
	// artifact cache dir + kubeconfig context — collision-free per bed, since several beds may
	// share one kind:vm entity); deploy_name is the real per-deploy identity the guest-forward
	// port-forward LEDGER lookup keys off (the same value domain-scopes artifact_key); vm_entity
	// is the SHARED kind:vm entity name (e.g. several beds' common `from: k3s-vm`), a DIFFERENT
	// identity space needed only to resolve the entity's DECLARED network.port_forwards
	// template. See this plugin's own k3s_post.go (deployVMForwards) — the former
	// charly/k8s_plugin.go core seam is deleted.
	artifact_key?: string @go(ArtifactKey)
	deploy_name?:  string @go(DeployName)
	vm_entity?:    string @go(VmEntity)
}
