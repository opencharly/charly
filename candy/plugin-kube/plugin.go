// Package kube is the charly plugin owning ALL Kubernetes
// cluster interaction: the `kube` cluster-probe check VERB, the `deploy:k8s`
// SUBSTRATE (the `target: k8s` workload deploy, F1 — `kubectl apply -k` on the
// host-generated Kustomize tree), AND the k3s kubeconfig-merge the k3s-server /
// target:k8s deploy seam needs (an importable root package + its own go.mod). It exists
// to keep the heavy k8s.io/client-go + k8s.io/apimachinery stack OUT of charly's
// core go.mod: the host go-builds this binary and serves it OUT-OF-PROCESS over
// go-plugin gRPC via the charly plugin SDK, so the `kube:` verb dispatches through
// the provider registry exactly like a built-in — the authored `kube: <method>` sugar
// desugars to plugin/plugin_input; the method + kube-exclusive fields ride the input
// map, validated against this plugin's own #KubeInput — and `target: k8s` resolves to
// this plugin's deploy:k8s provider over
// the E3b reverse channel (the host preresolves the cluster template + image
// Capabilities → the egress-validated Kustomize tree, k8s_deploy_preresolve.go).
// The goadb-analog of candy/plugin-adb: the FULL client-go/clientcmd/dynamic
// dependency + the single kubectl-apply path live HERE (R3).
//
// Dual-placement by construction: the SAME NewProvider()/NewMeta() compile INTO charly
// in-process when listed in compiled_plugins, or cmd/serve serves them OUT-OF-PROCESS
// over go-plugin gRPC when they are not — placement is invisible above the registry.
package kube

import (
	"embed"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
)

//go:embed schema/*.cue
var schemaFS embed.FS

// NewProvider returns the kube provider.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta advertises verb:kube + deploy:k8s + the plugin's self-contained CUE schema
// (via sdk.NewMeta → BuildCapabilities). The verb's plugin_input validates against the
// served #KubeInput (the method enum + every kube-exclusive modifier moved here from
// core #Op in the schema-compaction cutover); the deploy substrate keeps its authoring
// contract on core #Deploy / #K8s (the `k8s:` node + the `kubernetes:` block) and
// carries an EMPTY InputDef.
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta("2026.174.1200",
		[]sdk.ProvidedCapability{
			{Class: "verb", Word: "kube", InputDef: "#KubeInput", Primary: "method"},
			{Class: "deploy", Word: "k8s", InputDef: ""},
		},
		schemaFS)
}
