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
// kube_version/json) lives HERE. Only the genuinely SHARED step modifiers
// (timeout, the exit_status/stdout/stderr matchers, context, …) stay on core #Op,
// read off the step Op by the provider. The host preresolver still rewrites a
// `cluster:` profile to a concrete `kube_context` — now into the input map
// (charly/k8s_config.go) — and charly/k3s_post.go synthesizes the internal
// {method: merge-kubeconfig, kubeconfig, kube_context} input.
//
// SELF-CONTAINED: it references NO base def, so it compiles standalone (the SDK's
// serve-side check + gengotypes) AND splices onto the base (base ++ plugin is a
// def-name collision check, not a base-reference resolver).
//
// The plugin ALSO serves deploy:k8s (the `target: k8s` substrate) — that capability
// keeps its authoring contract on core #Deploy / #K8s and carries NO plugin_input,
// so no input def for it lives here.

// #KubeInput is the `kube` verb's plugin_input: the method name plus its
// method-exclusive modifiers.
#KubeInput: {
	// method — the kube method name (the former core #KubeMethod enum plus the
	// internal merge-kubeconfig the host synthesizes; the verb's PRIMARY input
	// field, so `kube: nodes` desugars to {method: "nodes"}).
	method: ("nodes" | "wait-nodes" | "pods" | "wait-ready" | "ingress" | "ingressclass" | "storageclass" | "service" | "lb-external-ip" | "addons" | "apply" | "delete" | "raw" | "merge-kubeconfig") @go(Method,type=string)
	// name / namespace / label — resource identity + selector.
	name?:      string
	namespace?: string
	label?:     string
	// cluster — a kind:k8s cluster template name; the HOST preresolves it to a
	// concrete kube_context (findK8sSpec needs the project loader) and leaves the
	// authored key in place, so the input def admits both.
	cluster?: string
	// manifest — the multi-doc YAML path (apply/delete).
	manifest?: string
	// kube_kind / kube_count — wait-ready's workload kind + wait-nodes' Ready count.
	kube_kind?:  string @go(KubeKind)
	kube_count?: int    @go(KubeCount,type=int)
	// kubeconfig / kube_context — the cluster-selection pair (kubeconfig path +
	// context; also the merge-kubeconfig payload).
	kubeconfig?:   string
	kube_context?: string @go(KubeContext)
	// kube_resource / kube_group / kube_version / json — the raw escape hatch's
	// GVR + JSON output toggle.
	kube_resource?: string @go(KubeResource)
	kube_group?:    string @go(KubeGroup)
	kube_version?:  string @go(KubeVersion)
	json?:          bool   @go(JSON)
}
