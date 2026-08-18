// Package kube is the charly plugin owning ALL Kubernetes
// cluster interaction: the `kube` cluster-probe check VERB, the `deploy:kubernetes`
// SUBSTRATE (the `target: kubernetes` workload deploy, F1 — `kubectl apply -k` on the
// plugin-generated Kustomize tree (materialize.go's materializeKustomize, on the host)), AND the k3s kubeconfig-merge the k3s-server /
// target:kubernetes deploy seam needs (an importable root package + its own go.mod). It exists
// to keep the heavy k8s.io/client-go + k8s.io/apimachinery stack OUT of charly's
// core go.mod: the host go-builds this binary and serves it OUT-OF-PROCESS over
// go-plugin gRPC via the charly plugin SDK, so the `kube:` verb dispatches through
// the provider registry exactly like a built-in — the authored `kube: <method>` sugar
// desugars to plugin/plugin_input; the method + kube-exclusive fields ride the input
// map, validated against this plugin's own #KubeInput — and `target: kubernetes` resolves to
// this plugin's deploy:kubernetes provider, reached via candy/plugin-fleet's generic
// Invoke(OpDeployDispatch) → sdk.Executor.InvokeProvider (S3b — was the core-side
// pluginDeployTarget-over-E3b path before the deploy-dispatch cluster moved) — THIS
// plugin's own preresolve.go (F6, FINAL/K5 unit 6a; dispatched directly by
// candy/plugin-fleet's preresolveSubstrate via InvokeProvider(OpPreresolve), S3b —
// the core-side deploy_preresolve.go:wireDeployPreresolver registry it used to route
// through is dissolved) resolves the cluster template + image Capabilities and GENERATES the
// egress-validated Kustomize tree ITSELF (materialize.go, K5-A item 6 — verb:k8sgen/verb:egress
// reached peer-to-peer via InvokeProvider, disk I/O done here directly; no host round trip),
// self-loading the project for the cluster/node lookup itself now too (K-wave W3a A3-phase-2:
// loaderkit.ResolveMergedTreeViaExecutor / ResolveKubernetesEntityViaExecutor) — no host round trip
// anywhere in this leg anymore.
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
	pb "github.com/opencharly/spec/proto"
)

//go:embed schema/*.cue
var schemaFS embed.FS

// NewProvider returns the kube provider.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta advertises verb:kube + deploy:kubernetes + the plugin's self-contained CUE schema
// (via sdk.NewMeta → BuildCapabilities). The verb's plugin_input validates against the
// served #KubeInput (the method enum + every kube-exclusive modifier moved here from
// core #Op in the schema-compaction cutover); the deploy substrate keeps its authoring
// contract on core #Deploy / #Kubernetes (the `kubernetes:` node + the `deploy:` block) and
// carries an EMPTY InputDef.
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta("2026.174.1200",
		[]sdk.ProvidedCapability{
			{Class: "verb", Word: "kube", InputDef: "#KubeInput", Primary: "method"},
			{Class: "deploy", Word: "kubernetes", InputDef: "", Preresolve: true},
		},
		schemaFS)
}
